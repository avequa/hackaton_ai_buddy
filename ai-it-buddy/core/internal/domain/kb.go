package domain

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"unicode"
)

// maxDocChars - сколько текста документа отдавать агенту: экономим токены бесплатного лимита модели
const maxDocChars = 1500

// DocHit - найденный документ с текстом для ответа и ссылкой на источник
type DocHit struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Service string  `json:"service"`
	Score   float64 `json:"score"`
	Content string  `json:"content"`
}

// tokenize - слова в нижнем регистре; короткие отбрасываются, у длинных берется основа (первые 6 букв),
// чтобы "подключается" и "подключиться" совпадали без морфологического словаря
func tokenize(s string) []string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := words[:0]
	for _, w := range words {
		r := []rune(w)
		if len(r) < 3 {
			continue
		}
		if len(r) > 6 {
			w = string(r[:6])
		}
		out = append(out, w)
	}
	return out
}

// synonyms - как стажеры пишут в чате и как написано в инструкциях. Ключ - основа слова (как в tokenize).
var synonyms = map[string]string{
	"впн": "vpn", "гит": "gitlab", "гитлаб": "gitlab", "графан": "grafana", "жира": "jira", "джира": "jira",
	"конфлю": "confluence", "кубер": "k8s", "тормоз": "медленно", "тупит": "медленно", "лагает": "медленно",
	"ноут": "ноутбук", "комп": "ноутбук", "мышка": "мышь", "наушни": "гарнитура",
}

// expandQuery - добавляет к запросу слова из инструкций для жаргона: «впн тормозит» ищется и как «vpn медленно»
func expandQuery(query string) string {
	var extra []string
	for _, w := range tokenize(query) {
		if s, ok := synonyms[w]; ok {
			extra = append(extra, s)
		}
	}
	if len(extra) == 0 {
		return query
	}
	return query + " " + strings.Join(extra, " ")
}

// SearchKB - поиск по базе знаний: в Postgres (полнотекстовый, русская морфология), если он подключен,
// иначе или при ошибке базы - в памяти. Помощник продолжает отвечать, даже когда база недоступна.
func (c *Core) SearchKB(ctx context.Context, query string, limit int) []DocHit {
	limit = min(max(limit, 1), 3)
	query = expandQuery(query)
	c.mu.Lock()
	kb := c.kb
	c.mu.Unlock()
	if kb != nil && strings.TrimSpace(query) != "" {
		hits, err := kb.SearchKB(ctx, query, limit)
		if err == nil {
			return hits
		}
		if c.opts.OnKBError != nil {
			c.opts.OnKBError(err)
		}
	}
	return c.searchMemory(query, limit)
}

// searchMemory - поиск без базы: совпадения в тегах весят больше, чем в заголовке, а в заголовке - больше, чем в тексте
func (c *Core) searchMemory(query string, limit int) []DocHit {
	q := tokenize(query)
	if len(q) == 0 {
		return nil
	}
	var hits []DocHit
	for _, d := range c.docs {
		tags := tokenize(strings.Join(d.Tags, " ") + " " + d.Service)
		title := tokenize(d.Title)
		body := tokenize(d.Body)
		score := 0.0
		for _, w := range q {
			switch {
			case slices.Contains(tags, w):
				score += 3
			case slices.Contains(title, w):
				score += 2
			case slices.Contains(body, w):
				score++
			}
		}
		if score == 0 {
			continue
		}
		content := d.Body
		if r := []rune(content); len(r) > maxDocChars {
			content = string(r[:maxDocChars]) + "..."
		}
		hits = append(hits, DocHit{ID: d.ID, Title: d.Title, Service: d.Service, Score: score / float64(len(q)), Content: content})
	}
	slices.SortFunc(hits, func(a, b DocHit) int { return cmp.Or(cmp.Compare(b.Score, a.Score), cmp.Compare(a.ID, b.ID)) })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}
