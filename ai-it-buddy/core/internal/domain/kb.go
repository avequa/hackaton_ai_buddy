package domain

import (
	"cmp"
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

// SearchKB - поиск по базе знаний: совпадения в тегах весят больше, чем в заголовке, а в заголовке - больше, чем в тексте.
// Это заглушка с тем же контрактом, что будет у гибридного поиска (pgvector + полнотекстовый Postgres).
func (c *Core) SearchKB(query string, limit int) []DocHit {
	q := tokenize(query)
	if len(q) == 0 {
		return nil
	}
	limit = min(max(limit, 1), 3)
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
