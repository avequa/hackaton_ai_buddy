package domain

import (
	"fmt"
	"slices"
)

// StepView - шаг адаптации со статусом для конкретного стажера
type StepView struct {
	Step
	Done   bool   `json:"done"`
	DoneBy string `json:"done_by,omitempty"`
	Hint   string `json:"hint,omitempty"` // кто или что закрывает шаг
}

// Onboarding - путь адаптации пользователя
type Onboarding struct {
	User     string     `json:"user"`
	Done     int        `json:"done"`
	Total    int        `json:"total"`
	Next     *StepView  `json:"next,omitempty"`
	Steps    []StepView `json:"steps"`
	OpenReqs []Request  `json:"open_requests"`
}

// confirmHints - подсказка, кто закрывает шаг
var confirmHints = map[string]string{
	ConfirmSelf:   "отметь сам, когда сделаешь",
	ConfirmMentor: "отмечает наставник",
	ConfirmLead:   "отмечает руководитель",
	ConfirmAuto:   "закроется сам, когда выдадут доступ",
}

// Onboarding - этапы, статусы и следующий шаг стажера
func (c *Core) Onboarding(userID string) (Onboarding, error) {
	c.mu.Lock()
	u, err := c.user(userID)
	if err != nil {
		c.mu.Unlock()
		return Onboarding{}, err
	}
	out := Onboarding{User: u.ID, Total: len(c.steps)}
	for _, s := range c.steps {
		v := StepView{Step: s, Hint: confirmHints[s.Confirm]}
		if ev, ok := c.stepsDone[u.ID][s.ID]; ok {
			v.Done, v.DoneBy = true, ev.By
		} else if s.Confirm == ConfirmAuto && s.Auto != nil && c.hasService(u.ID, s.Auto.Service) {
			v.Done, v.DoneBy = true, "система: доступ выдан"
		}
		if v.Done {
			out.Done++
		} else if out.Next == nil {
			next := v
			out.Next = &next
		}
		out.Steps = append(out.Steps, v)
	}
	c.mu.Unlock()

	for _, r := range c.MyRequests(u.ID, "") {
		if !r.Final() {
			out.OpenReqs = append(out.OpenReqs, r)
		}
	}
	return out, nil
}

// hasService - есть ли у пользователя хоть какой-то доступ к сервису
func (c *Core) hasService(userID, service string) bool {
	return slices.ContainsFunc(c.grants, func(g Grant) bool { return g.User == userID && g.Service == service })
}

// CompleteStep - отметить шаг: стажер - свои self-шаги, наставник и руководитель - шаги своего стажера
func (c *Core) CompleteStep(actorID, internID, stepID string) (StepView, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	actor, err := c.user(actorID)
	if err != nil {
		return StepView{}, err
	}
	intern, err := c.user(internID)
	if err != nil {
		return StepView{}, err
	}
	idx := slices.IndexFunc(c.steps, func(s Step) bool { return s.ID == stepID })
	if idx < 0 {
		return StepView{}, fmt.Errorf("step %q: %w", stepID, ErrNotFound)
	}
	step := c.steps[idx]

	allowed := false
	switch step.Confirm {
	case ConfirmSelf:
		allowed = actor.ID == intern.ID
	case ConfirmMentor:
		allowed = actor.ID == intern.Mentor || actor.ID == intern.Lead
	case ConfirmLead:
		allowed = actor.ID == intern.Lead
	}
	if !allowed {
		return StepView{}, fmt.Errorf("step %q is confirmed by %s: %w", stepID, step.Confirm, ErrForbidden)
	}
	if c.stepsDone[intern.ID] == nil {
		c.stepsDone[intern.ID] = map[string]Event{}
	}
	ev := Event{At: c.opts.Now(), Status: "done", By: actor.Name}
	c.stepsDone[intern.ID][step.ID] = ev
	c.dirty.steps[[2]string{intern.ID, step.ID}] = true
	return StepView{Step: step, Done: true, DoneBy: actor.Name, Hint: confirmHints[step.Confirm]}, nil
}

// MenteeSummary - стажер наставника или руководителя с прогрессом адаптации
type MenteeSummary struct {
	User          User      `json:"user"`
	Done          int       `json:"done"`
	Total         int       `json:"total"`
	Next          *StepView `json:"next,omitempty"`
	PendingReview int       `json:"pending_approvals"` // заявки "не по роли", ждущие решения этого наставника
}

// Mentees - стажеры, у которых пользователь наставник или руководитель
func (c *Core) Mentees(viewerID string) ([]MenteeSummary, error) {
	c.mu.Lock()
	if _, err := c.user(viewerID); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	var ids []string
	for _, u := range c.users {
		if u.Role == RoleIntern && (u.Mentor == viewerID || u.Lead == viewerID) {
			ids = append(ids, u.ID)
		}
	}
	c.mu.Unlock()
	slices.Sort(ids)

	pending := map[string]int{}
	for _, r := range c.Approvals(viewerID) {
		pending[r.User]++
	}
	out := make([]MenteeSummary, 0, len(ids))
	for _, id := range ids {
		ob, err := c.Onboarding(id)
		if err != nil {
			return nil, err
		}
		u, _ := c.User(id)
		out = append(out, MenteeSummary{User: u, Done: ob.Done, Total: ob.Total, Next: ob.Next, PendingReview: pending[id]})
	}
	return out, nil
}

// OnboardingFor - путь адаптации стажера глазами наставника или руководителя; остальным стажер "не найден"
func (c *Core) OnboardingFor(viewerID, internID string) (Onboarding, error) {
	c.mu.Lock()
	viewer, err := c.user(viewerID)
	if err != nil {
		c.mu.Unlock()
		return Onboarding{}, err
	}
	intern, ok := c.users[internID]
	c.mu.Unlock()
	if !ok {
		return Onboarding{}, fmt.Errorf("user %q: %w", internID, ErrNotFound)
	}
	if viewer.ID != intern.ID && intern.Mentor != viewer.ID && intern.Lead != viewer.ID {
		return Onboarding{}, fmt.Errorf("user %q: %w", internID, ErrForeign)
	}
	return c.Onboarding(internID)
}
