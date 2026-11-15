package domain

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Digest - сводка для наставника по стажеру: без текста переписки, только факты
type Digest struct {
	Intern         User            `json:"intern"`
	Week           int             `json:"week"` // какая сейчас неделя стажировки
	Done           int             `json:"done"`
	Total          int             `json:"total"`
	Next           *StepView       `json:"next,omitempty"`
	Overdue        []StepView      `json:"overdue"`          // шаги прошлых недель, которые не сделаны
	WaitingForYou  []Request       `json:"waiting_for_you"`  // заявки и шаги, где нужен этот наставник
	StepsToConfirm []StepView      `json:"steps_to_confirm"` // шаги, которые отмечает наставник или руководитель
	StuckRequests  []Request       `json:"stuck_requests"`   // открыты дольше суток
	Rejected       []Request       `json:"rejected"`         // отказы - стоит обсудить на 1:1
	Topics         []TopicCount    `json:"topics"`           // с чем приходил к помощнику
	UnansweredInKB int             `json:"unanswered_in_kb"` // сколько раз база знаний не помогла
	SecurityEvents []SecurityEvent `json:"security_events"`  // попытки выйти за права
	TalkingPoints  []string        `json:"talking_points"`   // что обсудить на встрече 1:1
}

// TopicCount - тема и сколько раз
type TopicCount struct {
	Topic string `json:"topic"`
	Count int    `json:"count"`
}

const stuckAfter = 24 * time.Hour

func currentWeek(u User, now time.Time) int {
	start, err := time.Parse("2006-01-02", u.StartDate)
	if err != nil || now.Before(start) {
		return 1
	}
	return int(now.Sub(start).Hours()/24)/7 + 1
}

// Digest - сводка по стажеру для его наставника или руководителя
func (c *Core) Digest(viewerID, internID string) (Digest, error) {
	ob, err := c.OnboardingFor(viewerID, internID)
	if err != nil {
		return Digest{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	intern, viewer := c.users[internID], c.users[viewerID]
	now := c.opts.Now()
	d := Digest{
		Intern: intern, Week: currentWeek(intern, now), Done: ob.Done, Total: ob.Total, Next: ob.Next,
		Overdue: []StepView{}, WaitingForYou: []Request{}, StepsToConfirm: []StepView{}, StuckRequests: []Request{},
		Rejected: []Request{}, Topics: []TopicCount{}, SecurityEvents: []SecurityEvent{}, UnansweredInKB: c.kbMisses[internID],
	}
	for _, s := range ob.Steps {
		if s.Done {
			continue
		}
		if s.Week < d.Week {
			d.Overdue = append(d.Overdue, s)
		}
		if (s.Confirm == ConfirmMentor && viewer.ID == intern.Mentor) || (s.Confirm == ConfirmLead && viewer.ID == intern.Lead) ||
			(s.Confirm == ConfirmMentor && viewer.ID == intern.Lead && intern.Mentor == "") {
			d.StepsToConfirm = append(d.StepsToConfirm, s)
		}
	}
	for _, r := range c.requests {
		if r.User != internID {
			continue
		}
		switch {
		case r.Status == StatusPendingMentor && r.Approver == viewerID:
			d.WaitingForYou = append(d.WaitingForYou, *r)
		case r.Status == StatusRejected && now.Sub(r.UpdatedAt) < 7*24*time.Hour:
			d.Rejected = append(d.Rejected, *r)
		case !r.Final() && r.Status != StatusDraft && now.Sub(r.CreatedAt) > stuckAfter:
			d.StuckRequests = append(d.StuckRequests, *r)
		}
	}
	for _, list := range [][]Request{d.WaitingForYou, d.Rejected, d.StuckRequests} {
		sortRequests(list)
	}
	for topic, n := range c.activity[internID] {
		d.Topics = append(d.Topics, TopicCount{Topic: topic, Count: n})
	}
	slices.SortFunc(d.Topics, func(a, b TopicCount) int { return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Topic, b.Topic)) })
	for _, ev := range slices.Backward(c.security) {
		if ev.User == internID {
			d.SecurityEvents = append(d.SecurityEvents, ev)
		}
	}
	d.TalkingPoints = talkingPoints(d)
	return d, nil
}

func talkingPoints(d Digest) []string {
	out := []string{} // [] а не null: фронт проверяет схему
	if n := len(d.WaitingForYou); n > 0 {
		out = append(out, fmt.Sprintf("Решить по %d заявк(ам) не по роли - стажер ждет", n))
	}
	if n := len(d.StepsToConfirm); n > 0 {
		out = append(out, fmt.Sprintf("Отметить шаги, которые подтверждаете вы: %d", n))
	}
	if len(d.Overdue) > 0 {
		titles := make([]string, 0, len(d.Overdue))
		for _, s := range d.Overdue {
			titles = append(titles, s.Title)
		}
		out = append(out, "Отстает от плана: "+strings.Join(titles, "; "))
	}
	if len(d.Rejected) > 0 {
		out = append(out, fmt.Sprintf("Обсудить отказы по заявкам (%d): понимает ли стажер, что делать дальше", len(d.Rejected)))
	}
	if len(d.StuckRequests) > 0 {
		out = append(out, fmt.Sprintf("Заявки висят дольше суток (%d) - возможно, нужно напомнить согласующему", len(d.StuckRequests)))
	}
	if d.UnansweredInKB > 0 {
		out = append(out, fmt.Sprintf("База знаний не помогла %d раз - каких инструкций не хватает", d.UnansweredInKB))
	}
	if len(d.SecurityEvents) > 0 {
		out = append(out, fmt.Sprintf("Попытки выйти за права: %d - объяснить, как устроены доступы", len(d.SecurityEvents)))
	}
	if len(out) == 0 {
		out = append(out, "По плану, вопросов нет - можно говорить о задачах, а не об организационном")
	}
	return out
}

// SendDigests - уведомляет каждого наставника короткой сводкой по каждому его стажеру.
// В Авито это задача для ИИ-крона раз в неделю; здесь - фоновая задача ядра и ручка для демо.
func (c *Core) SendDigests() int {
	c.mu.Lock()
	var pairs [][2]string
	for _, u := range c.users {
		if u.Role == RoleIntern && u.Mentor != "" {
			pairs = append(pairs, [2]string{u.Mentor, u.ID})
		}
	}
	c.mu.Unlock()
	slices.SortFunc(pairs, func(a, b [2]string) int { return cmp.Compare(a[1], b[1]) })
	sent := 0
	for _, p := range pairs {
		d, err := c.Digest(p[0], p[1])
		if err != nil {
			continue
		}
		text := fmt.Sprintf("Неделя %d, шагов %d из %d. %s", d.Week, d.Done, d.Total, strings.Join(d.TalkingPoints, ". "))
		c.mu.Lock()
		c.notify(p[0], NoteDigest, "Сводка: "+d.Intern.Name, text, d.Intern.ID)
		c.mu.Unlock()
		sent++
	}
	return sent
}

// StarterItem - что вошло или не вошло в стартовый набор
type StarterItem struct {
	Service string   `json:"service"`
	Project string   `json:"project"`
	Level   string   `json:"level"`
	Outcome string   `json:"outcome"` // draft_created | already_granted | already_requested
	Request *Request `json:"request,omitempty"`
}

// starterServices - что входит в стартовый набор стажера: доступ на чтение к инструментам своей команды
var starterServices = []string{"vpn", "gitlab", "jira", "confluence"}

// StarterPack - черновики стандартных доступов по роли и команде. Подтверждаются одной кнопкой (ConfirmMany).
func (c *Core) StarterPack(userID string) ([]StarterItem, error) {
	c.mu.Lock()
	u, err := c.user(userID)
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	var want []AccessDetails
	for _, sid := range starterServices {
		s, ok := c.services[sid]
		if !ok || !slices.Contains(c.profile(u).TypicalAccess[sid], LevelRead) {
			continue
		}
		for _, p := range s.Projects {
			if p.Team == u.Team || p.Team == "*" {
				want = append(want, AccessDetails{
					Service: s.ID, Project: p.ID, Level: LevelRead,
					Justification: "стартовый набор: доступ к инструментам своей команды",
				})
			}
		}
	}
	c.starter[u.ID] = true
	c.dirty.flags[u.ID] = true
	c.mu.Unlock()

	out := make([]StarterItem, 0, len(want))
	for _, a := range want {
		res, err := c.DraftAccess(u.ID, a)
		if err != nil {
			return nil, err
		}
		out = append(out, StarterItem{Service: a.Service, Project: a.Project, Level: a.Level, Outcome: res.Outcome, Request: res.Request})
	}
	return out, nil
}

// OfferStarterPack - при первом входе стажера: уведомление, что стартовый набор можно получить одной кнопкой
func (c *Core) OfferStarterPack(userID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	u, ok := c.users[userID]
	if !ok || u.Role != RoleIntern || c.starter[userID] {
		return false
	}
	c.starter[userID] = true
	c.dirty.flags[userID] = true
	c.notify(userID, NoteStarterPack, "Стартовый набор доступов",
		"Подготовить заявки на VPN, GitLab, Jira и Confluence твоей команды? Подтвердишь их одной кнопкой.", "")
	return true
}

// ConfirmResult - итог подтверждения одной заявки из пачки
type ConfirmResult struct {
	ID      string   `json:"id"`
	Request *Request `json:"request,omitempty"`
	Error   string   `json:"error,omitempty"`
	err     error
}

// Err - исходная ошибка (для errors.Is на стороне API)
func (r ConfirmResult) Err() error { return r.err }

// ConfirmMany - подтвердить несколько своих черновиков (стартовый набор, несколько заявок из чата)
func (c *Core) ConfirmMany(userID string, ids []string) []ConfirmResult {
	out := make([]ConfirmResult, 0, len(ids))
	for _, id := range ids {
		r, err := c.Confirm(userID, id)
		if err != nil {
			out = append(out, ConfirmResult{ID: id, Error: err.Error(), err: err})
			continue
		}
		out = append(out, ConfirmResult{ID: id, Request: &r})
	}
	return out
}
