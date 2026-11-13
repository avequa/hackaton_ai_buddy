package domain

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// ServiceView - сервис из каталога глазами пользователя: с его текущим доступом по проектам
type ServiceView struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	TypicalTasks []string      `json:"typical_tasks"`
	Levels       []string      `json:"levels"`
	Projects     []ProjectView `json:"projects"`
}

// ProjectView - проект и уровень доступа пользователя к нему ("" - доступа нет)
type ProjectView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Team     string `json:"team"`
	MyAccess string `json:"my_access,omitempty"`
}

// Services - каталог сервисов, чтобы агент сопоставил задачу стажера с нужными системами
func (c *Core) Services(userID string) ([]ServiceView, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.user(userID); err != nil {
		return nil, err
	}
	out := make([]ServiceView, 0, len(c.services))
	for _, s := range c.services {
		v := ServiceView{ID: s.ID, Name: s.Name, Description: s.Description, TypicalTasks: s.TypicalTasks}
		for level := range s.Approvers {
			v.Levels = append(v.Levels, level)
		}
		slices.SortFunc(v.Levels, func(a, b string) int { return cmp.Compare(levelRank[a], levelRank[b]) })
		for _, p := range s.Projects {
			v.Projects = append(v.Projects, ProjectView{ID: p.ID, Name: p.Name, Team: p.Team, MyAccess: c.grantLevel(userID, s.ID, p.ID)})
		}
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b ServiceView) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// MyAccess - выданные доступы и незакрытые заявки на доступ
func (c *Core) MyAccess(userID string) ([]Grant, []Request, error) {
	c.mu.Lock()
	if _, err := c.user(userID); err != nil {
		c.mu.Unlock()
		return nil, nil, err
	}
	var grants []Grant
	for _, g := range c.grants {
		if g.User == userID {
			grants = append(grants, g)
		}
	}
	c.mu.Unlock()

	var open []Request
	for _, r := range c.MyRequests(userID, KindAccess) {
		if !r.Final() {
			open = append(open, r)
		}
	}
	return grants, open, nil
}

// grantLevel - максимальный выданный уровень доступа к проекту
func (c *Core) grantLevel(userID, service, project string) string {
	best := ""
	for _, g := range c.grants {
		if g.User == userID && g.Service == service && g.Project == project && levelRank[g.Level] > levelRank[best] {
			best = g.Level
		}
	}
	return best
}

// grant - выдает доступ (повышает уровень, если доступ уже был)
func (c *Core) grant(userID string, a AccessDetails) {
	for i, g := range c.grants {
		if g.User == userID && g.Service == a.Service && g.Project == a.Project {
			if levelRank[a.Level] > levelRank[g.Level] {
				c.grants[i].Level = a.Level
			}
			return
		}
	}
	c.grants = append(c.grants, Grant{User: userID, Service: a.Service, Project: a.Project, Level: a.Level})
}

// lookupProject - сервис и проект из каталога
func (c *Core) lookupProject(serviceID, projectID string) (Service, Project, error) {
	s, ok := c.services[serviceID]
	if !ok {
		ids := make([]string, 0, len(c.services))
		for id := range c.services {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		return Service{}, Project{}, fmt.Errorf("unknown service %q, available: %s: %w", serviceID, strings.Join(ids, ", "), ErrInvalid)
	}
	for _, p := range s.Projects {
		if p.ID == projectID {
			return s, p, nil
		}
	}
	ids := make([]string, 0, len(s.Projects))
	for _, p := range s.Projects {
		ids = append(ids, p.ID)
	}
	return Service{}, Project{}, fmt.Errorf("unknown project %q in %s, available: %s: %w", projectID, serviceID, strings.Join(ids, ", "), ErrInvalid)
}

// accessApprover - кто согласует уровень доступа: auto, владелец проекта, руководитель или DevOps
func (c *Core) accessApprover(u User, a AccessDetails) string {
	s, p, err := c.lookupProject(a.Service, a.Project)
	if err != nil {
		return ApproverDevOps
	}
	switch s.Approvers[a.Level] {
	case ApproverAuto:
		return ApproverAuto
	case ApproverOwner:
		return p.Owner
	case ApproverLead:
		return cmp.Or(u.Lead, p.Owner)
	default:
		return ApproverDevOps
	}
}

// accessPolicy - мягкая политика: запрос сверх типичного для роли не запрещен, но сначала идет к наставнику
func (c *Core) accessPolicy(u User, s Service, p Project, level string) Policy {
	var reasons []string
	if !slices.Contains(c.profile(u).TypicalAccess[s.ID], level) {
		reasons = append(reasons, fmt.Sprintf("доступ %s к %s не типичен для роли %s/%s", level, s.Name, u.Role, u.Position))
	}
	if p.Team != "*" && p.Team != u.Team {
		reasons = append(reasons, fmt.Sprintf("проект %s принадлежит команде %s, а ты в команде %s", p.ID, p.Team, u.Team))
	}
	if len(reasons) > 0 {
		return Policy{Verdict: VerdictNeedsMentor, Reasons: reasons}
	}
	return Policy{Verdict: VerdictOK}
}

// AccessDraft - результат попытки создать заявку на доступ
type AccessDraft struct {
	Outcome string   `json:"outcome"` // draft_created | already_granted | already_requested
	Request *Request `json:"request,omitempty"`
	Level   string   `json:"current_level,omitempty"`
}

// DraftAccess - черновик заявки на доступ. Права, команда и дубликаты проверяет сервер, а не модель.
func (c *Core) DraftAccess(userID string, a AccessDetails) (AccessDraft, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	u, err := c.user(userID)
	if err != nil {
		return AccessDraft{}, err
	}
	a.Justification = strings.TrimSpace(a.Justification)
	if a.Justification == "" {
		return AccessDraft{}, fmt.Errorf("justification is required: for which task the access is needed: %w", ErrInvalid)
	}
	s, p, err := c.lookupProject(a.Service, a.Project)
	if err != nil {
		return AccessDraft{}, err
	}
	if _, ok := s.Approvers[a.Level]; !ok {
		return AccessDraft{}, fmt.Errorf("level %q is not available for %s: %w", a.Level, s.ID, ErrInvalid)
	}

	if have := c.grantLevel(u.ID, s.ID, p.ID); levelRank[have] >= levelRank[a.Level] {
		return AccessDraft{Outcome: "already_granted", Level: have}, nil
	}
	for _, r := range c.requests {
		if r.User == u.ID && r.Kind == KindAccess && !r.Final() && r.Access.Service == s.ID && r.Access.Project == p.ID {
			existing := *r
			return AccessDraft{Outcome: "already_requested", Request: &existing}, nil
		}
	}

	r := c.newRequest(KindAccess, u, fmt.Sprintf("Доступ %s к %s / %s", a.Level, s.Name, p.Name))
	r.Access = &a
	r.Policy = c.accessPolicy(u, s, p, a.Level)
	r.Approver = c.accessApprover(u, a)
	r.ApproverName = c.name(r.Approver)
	draft := *r
	return AccessDraft{Outcome: "draft_created", Request: &draft}, nil
}

// decideAccess - решение согласующего (имитация): admin и прод стажерам не выдаются, остальное одобряется
func (c *Core) decideAccess(r *Request) {
	u := c.users[r.User]
	switch {
	case u.Role == RoleIntern && r.Access.Level == LevelAdmin:
		r.Resolution = "Права администратора стажерам не выдаются. Для задачи хватит уровня write - оформи заявку на него."
	case u.Role == RoleIntern && r.Access.Service == "k8s-prod":
		r.Resolution = "Доступ к продовому кластеру есть только у дежурных SRE. Логи и метрики сервиса смотри в Grafana."
	default:
		c.grant(u.ID, *r.Access)
		c.move(r, StatusApproved, r.ApproverName, "доступ выдан")
		return
	}
	c.move(r, StatusRejected, r.ApproverName, r.Resolution)
}
