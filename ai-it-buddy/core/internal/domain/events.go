package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Виды уведомлений в ленте чата
const (
	NoteIncident         = "incident"          // SRE объявили сбой
	NoteIncidentResolved = "incident_resolved" // сбой устранен
	NoteRequest          = "request"           // статус моей заявки изменился
	NoteApproval         = "approval"          // наставнику: заявка "не по роли" ждет решения
	NoteSecurity         = "security"          // наставнику и руководителю: стажер пытается выйти за права
	NoteDigest           = "digest"            // наставнику: еженедельная сводка по стажеру
	NoteStarterPack      = "starter_pack"      // стажеру: стартовый набор доступов готов
)

// Notification - сообщение в ленте пользователя; ref - id заявки, инцидента или стажера
type Notification struct {
	ID        int64     `json:"id"`
	User      string    `json:"-"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Text      string    `json:"text"`
	Ref       string    `json:"ref,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Read      bool      `json:"read"`
}

// notify - уведомление одному пользователю (под блокировкой)
func (c *Core) notify(userID, kind, title, text, ref string) {
	if _, ok := c.users[userID]; !ok {
		return // devops, it-support и прочие роли без учетки
	}
	c.noteSeq++
	c.notes = append(c.notes, Notification{
		ID: c.noteSeq, User: userID, Kind: kind, Title: title, Text: text, Ref: ref, CreatedAt: c.opts.Now(),
	})
	c.dirty.notes[c.noteSeq] = true
}

// broadcast - уведомление всем (под блокировкой)
func (c *Core) broadcast(kind, title, text, ref string) {
	ids := make([]string, 0, len(c.users))
	for id := range c.users {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		c.notify(id, kind, title, text, ref)
	}
}

// statusText - как статус выглядит в уведомлении автору
var statusText = map[string]string{
	StatusInReview: "наставник одобрил, заявка у согласующего", StatusApproved: "одобрена",
	StatusRejected: "отклонена", StatusInProgress: "взята в работу", StatusReady: "готово к выдаче",
}

// notifyStatus - автору заявки: статус изменился (под блокировкой)
func (c *Core) notifyStatus(r *Request) {
	text := statusText[r.Status]
	if text == "" {
		return
	}
	if r.Resolution != "" {
		text += ". " + r.Resolution
	}
	c.notify(r.User, NoteRequest, r.ID+": "+r.Title, text, r.ID)
}

// Notifications - лента пользователя после id after (0 - вся), старые сверху, не больше 100
func (c *Core) Notifications(userID string, after int64) []Notification {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Notification
	for _, n := range c.notes {
		if n.User == userID && n.ID > after {
			out = append(out, n)
		}
	}
	if len(out) > 100 {
		out = out[len(out)-100:]
	}
	return out
}

// MarkRead - отметить прочитанными уведомления до id включительно
func (c *Core) MarkRead(userID string, upTo int64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for i := range c.notes {
		if c.notes[i].User == userID && c.notes[i].ID <= upTo && !c.notes[i].Read {
			c.notes[i].Read = true
			c.dirty.notes[c.notes[i].ID] = true
			n++
		}
	}
	return n
}

// Виды событий безопасности
const (
	SecForeign   = "foreign_object" // открыть или изменить чужую заявку, чужой шаг, чужого стажера
	SecForbidden = "forbidden"      // действие не по своей роли (отметить шаг наставника, объявить инцидент)
	SecChat      = "chat_attack"    // попытка уговорить помощника обойти правила
)

// alertCooldown - не будить наставника чаще раза в 10 минут по одному стажеру и виду события
const alertCooldown = 10 * time.Minute

// SecurityEvent - попытка выйти за права
type SecurityEvent struct {
	ID     int       `json:"id"`
	User   string    `json:"user"`
	Name   string    `json:"name"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail"`
	At     time.Time `json:"at"`
}

// secTitles - заголовок уведомления наставнику
var secTitles = map[string]string{
	SecForeign:   "Попытка открыть чужие данные",
	SecForbidden: "Попытка действия не по роли",
	SecChat:      "Попытка обойти ограничения помощника",
}

// RecordSecurity - фиксирует попытку и уведомляет наставника и руководителя стажера.
// Сами действия уже заблокированы сервером; это сигнал людям, а не защита.
func (c *Core) RecordSecurity(userID, kind, detail string) (SecurityEvent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	u, ok := c.users[userID]
	if !ok {
		return SecurityEvent{}, false
	}
	ev := SecurityEvent{ID: len(c.security) + 1, User: u.ID, Name: u.Name, Kind: kind, Detail: truncate(detail, 160), At: c.opts.Now()}
	c.security = append(c.security, ev)

	key := u.ID + "|" + kind
	if last, ok := c.alerted[key]; ok && ev.At.Sub(last) < alertCooldown {
		return ev, false
	}
	c.alerted[key] = ev.At
	for _, to := range []string{u.Mentor, u.Lead} {
		if to != "" {
			c.notify(to, NoteSecurity, secTitles[kind]+": "+u.Name, ev.Detail, u.ID)
		}
	}
	return ev, true
}

// SecurityEvents - события стажеров, у которых viewer наставник или руководитель; свои события не показываем
func (c *Core) SecurityEvents(viewerID string) []SecurityEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []SecurityEvent
	for _, ev := range slices.Backward(c.security) {
		u := c.users[ev.User]
		if u.Mentor == viewerID || u.Lead == viewerID {
			out = append(out, ev)
		}
	}
	return out
}

// chatAttacks - признаки попытки обойти правила через текст. Это эвристика для сигнала наставнику:
// сработает и на шутку, и пропустит изобретательную атаку. Защита - проверки прав на сервере.
var chatAttacks = []struct {
	re   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`(?i)(игнорир|забудь|отмени)\S*\s+(все\s+)?(свои\s+)?(предыдущ|прошл|сво|систем)\S*\s+(инструкц|правил|указан)`), "просит игнорировать инструкции"},
	{regexp.MustCompile(`(?i)ignore\s+(all\s+)?(the\s+)?(previous|prior|above|your)\s+(instructions|rules)`), "просит игнорировать инструкции"},
	{regexp.MustCompile(`(?i)(систем\S*\s+промпт|system\s+prompt|свои\s+инструкции\s+целиком)`), "выпытывает системный промпт"},
	{regexp.MustCompile(`(?i)(режим\S*\s+разработчик|developer\s+mode|jailbreak|\bDAN\b)`), "включает «режим без ограничений»"},
	{regexp.MustCompile(`(?i)(^|[\s,.!?])я\s+(теперь\s+|сейчас\s+)?(админ|администратор|наставник|руководитель|девопс|devops|sre)([\s,.!?]|$)`), "выдает себя за другую роль"},
	{regexp.MustCompile(`(?i)(без\s+подтвержден|не\s+надо\s+черновик|сразу\s+отправ)`), "просит отправить заявку без подтверждения"},
}

// ScreenChat - проверяет сообщение стажера перед агентом. Сообщение все равно уходит агенту:
// права проверяет сервер, а блокировка по эвристике мешала бы честным вопросам.
func (c *Core) ScreenChat(userID, message string) []string {
	var found []string
	for _, a := range chatAttacks {
		if a.re.MatchString(message) && !slices.Contains(found, a.what) {
			found = append(found, a.what)
		}
	}
	c.mu.Lock()
	if u, ok := c.users[userID]; ok {
		if other, hit := c.mentionsIntern(u, message); hit {
			found = append(found, "спрашивает о данных другого стажера ("+other+")")
		}
	}
	c.mu.Unlock()
	if len(found) > 0 {
		c.RecordSecurity(userID, SecChat, strings.Join(found, "; ")+": «"+truncate(message, 80)+"»")
	}
	return found
}

// mentionsIntern - упоминает ли сообщение другого стажера: по id или по имени в любом падеже
// (общая основа слова и имени: «Павла», «Павлу» ~ «Павел»). Под блокировкой.
func (c *Core) mentionsIntern(u User, message string) (string, bool) {
	lower := strings.ToLower(message)
	words := strings.FieldsFunc(lower, func(r rune) bool { return !unicode.IsLetter(r) && r != '.' })
	ids := make([]string, 0, len(c.users))
	for id := range c.users {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		other := c.users[id]
		if other.ID == u.ID || other.Role != RoleIntern {
			continue
		}
		if strings.Contains(lower, other.ID) {
			return other.Name, true
		}
		first := []rune(strings.ToLower(strings.Fields(other.Name)[0]))
		for _, w := range words {
			if r := []rune(w); len(r) >= 4 && len(first) >= 4 && commonPrefix(r, first) >= max(3, len(first)-2) {
				return other.Name, true
			}
		}
	}
	return "", false
}

// commonPrefix - длина общего начала двух слов
func commonPrefix(a, b []rune) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// truncate - обрезка по символам, а не байтам
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "..."
}

// TrackTopic - тема, с которой стажер приходил (для сводки наставнику). Текст переписки не храним.
func (c *Core) TrackTopic(userID, topic string) {
	if topic == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.activity[userID] == nil {
		c.activity[userID] = map[string]int{}
	}
	c.activity[userID][topic]++
	c.dirty.topics[[2]string{userID, topic}] = true
}

// TrackKBMiss - база знаний не нашла ответа: сигнал наставнику, какой инструкции не хватает
func (c *Core) TrackKBMiss(userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kbMisses[userID]++
	c.dirty.flags[userID] = true
}

// String - для логов
func (e SecurityEvent) String() string { return fmt.Sprintf("%s %s: %s", e.User, e.Kind, e.Detail) }
