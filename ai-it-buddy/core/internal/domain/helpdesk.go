package domain

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Health - состояние сервиса глазами IT Assistant
type Health struct {
	Service  string    `json:"service"`
	Status   string    `json:"status"` // ok | incident
	Incident *Incident `json:"incident,omitempty"`
}

// ServiceHealth - есть ли сейчас известный сбой сервиса. Источник в демо - список инцидентов,
// дальше сюда подключается incident-radar (анализ nginx-логов) и алерты Prometheus.
func (c *Core) ServiceHealth(service string) Health {
	c.mu.Lock()
	defer c.mu.Unlock()
	if inc, ok := c.incidents[service]; ok {
		return Health{Service: service, Status: "incident", Incident: &inc}
	}
	return Health{Service: service, Status: "ok"}
}

// serviceName - название сервиса для уведомлений (под блокировкой)
func (c *Core) serviceName(id string) string {
	if s, ok := c.services[id]; ok && s.Name != "" {
		return s.Name
	}
	return id
}

// KnownService - сервис есть в каталоге
func (c *Core) KnownService(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.services[id]
	return ok
}

// SetIncident - включает или снимает инцидент сервиса от имени демо-режима (вместо мониторинга)
func (c *Core) SetIncident(service, title string, active bool) Health {
	if active {
		_, _ = c.openIncident("", service, cmp.Or(strings.TrimSpace(title), "Сервис недоступен"), "")
	} else {
		c.mu.Lock()
		if inc, ok := c.incidents[service]; ok {
			c.resolve(inc.ID, "демо")
		}
		c.mu.Unlock()
	}
	return c.ServiceHealth(service)
}

// OpenIncident - SRE или руководитель объявляет сбой: всем пользователям сразу приходит уведомление в чат,
// а помощник начинает отвечать «это не у тебя, это инцидент» и не дает завести дубликат тикета
func (c *Core) OpenIncident(byID, service, title, message string) (Incident, error) {
	c.mu.Lock()
	by, err := c.user(byID)
	c.mu.Unlock()
	if err != nil {
		return Incident{}, err
	}
	if by.Role != RoleSRE && by.Role != RoleLead {
		return Incident{}, fmt.Errorf("only SRE or lead can declare an incident: %w", ErrForbidden)
	}
	return c.openIncident(by.Name, service, title, message)
}

// openIncident - общая часть для SRE, мониторинга и демо; by пустой - "мониторинг"
func (c *Core) openIncident(by, service, title, message string) (Incident, error) {
	service, title = strings.TrimSpace(service), strings.TrimSpace(title)
	if service == "" || title == "" {
		return Incident{}, fmt.Errorf("service and title are required: %w", ErrInvalid)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if inc, ok := c.incidents[service]; ok {
		return Incident{}, fmt.Errorf("incident %s for %s is already open: %w", inc.ID, service, ErrConflict)
	}
	inc := Incident{
		ID:      fmt.Sprintf("INC-%d", 100+len(c.incidentLog)+1),
		Service: service, Title: title, Message: strings.TrimSpace(message),
		Since: c.opts.Now(), By: cmp.Or(by, "мониторинг"),
	}
	c.incidents[service] = inc
	logged := inc
	c.incidentLog = append(c.incidentLog, &logged)
	c.dirty.incidents[inc.ID] = true
	text := inc.Title
	if inc.Message != "" {
		text += ". " + inc.Message
	}
	c.broadcast(NoteIncident, "Сбой: "+c.serviceName(service), text, inc.ID)
	return inc, nil
}

// ResolveIncident - сбой устранен, всем приходит уведомление
func (c *Core) ResolveIncident(byID, id string) (Incident, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	by, err := c.user(byID)
	if err != nil {
		return Incident{}, err
	}
	if by.Role != RoleSRE && by.Role != RoleLead {
		return Incident{}, fmt.Errorf("only SRE or lead can resolve an incident: %w", ErrForbidden)
	}
	inc, ok := c.resolve(id, by.Name)
	if !ok {
		return Incident{}, fmt.Errorf("open incident %s: %w", id, ErrNotFound)
	}
	return inc, nil
}

// resolve - закрывает открытый инцидент по id (под блокировкой)
func (c *Core) resolve(id, by string) (Incident, bool) {
	for _, inc := range c.incidentLog {
		if inc.ID != id || inc.ResolvedAt != nil {
			continue
		}
		now := c.opts.Now()
		inc.ResolvedAt = &now
		delete(c.incidents, inc.Service)
		c.dirty.incidents[inc.ID] = true
		c.broadcast(NoteIncidentResolved, "Восстановлено: "+c.serviceName(inc.Service), inc.Title+" - устранено ("+by+")", inc.ID)
		return *inc, true
	}
	return Incident{}, false
}

// Incidents - открытые инциденты и закрытые за последние сутки, новые сверху
func (c *Core) Incidents() []Incident {
	c.mu.Lock()
	defer c.mu.Unlock()
	day := c.opts.Now().Add(-24 * time.Hour)
	var out []Incident
	for _, inc := range slices.Backward(c.incidentLog) {
		if inc.ResolvedAt == nil || inc.ResolvedAt.After(day) {
			out = append(out, *inc)
		}
	}
	return out
}

// TicketDraft - результат попытки создать обращение
type TicketDraft struct {
	Outcome  string    `json:"outcome"` // draft_created | known_incident
	Request  *Request  `json:"request,omitempty"`
	Incident *Incident `json:"incident,omitempty"`
}

// DraftTicket - черновик обращения в поддержку с историей диагностики.
// Если по сервису уже идет инцидент, обращение не создается: поддержка и так чинит, дубликаты ей не нужны.
func (c *Core) DraftTicket(userID string, t TicketDetails) (TicketDraft, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	u, err := c.user(userID)
	if err != nil {
		return TicketDraft{}, err
	}
	t.Summary = strings.TrimSpace(t.Summary)
	if t.Summary == "" {
		return TicketDraft{}, fmt.Errorf("summary is required: %w", ErrInvalid)
	}
	if inc, ok := c.incidents[t.Service]; ok {
		return TicketDraft{Outcome: "known_incident", Incident: &inc}, nil
	}
	r := c.newRequest(KindHelpdesk, u, t.Summary)
	r.Ticket = &t
	r.Approver, r.ApproverName = "it-support", "IT-поддержка"
	draft := *r
	return TicketDraft{Outcome: "draft_created", Request: &draft}, nil
}

// RadarName - автор инцидентов, найденных по логам автоматически
const RadarName = "incident-radar"

// AutoOpenIncident - incident-radar увидел всплеск 5xx в логах сервиса. Если сбой уже объявлен
// (человеком или радаром), ничего не делает: opened=false.
func (c *Core) AutoOpenIncident(service, summary, message string) (inc Incident, opened bool, err error) {
	c.mu.Lock()
	_, ok := c.services[service]
	_, open := c.incidents[service]
	c.mu.Unlock()
	if !ok {
		return Incident{}, false, fmt.Errorf("service %s: %w", service, ErrNotFound)
	}
	if open {
		return Incident{}, false, nil
	}
	inc, err = c.openIncident(RadarName, service, summary, message)
	if errors.Is(err, ErrConflict) { // успели объявить вручную между проверкой и открытием
		return Incident{}, false, nil
	}
	return inc, err == nil, err
}

// AutoResolveIncident - ошибки в логах ушли. Закрывает только инцидент, открытый радаром:
// объявленный человеком сбой закрывает человек.
func (c *Core) AutoResolveIncident(service string) (Incident, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	inc, ok := c.incidents[service]
	if !ok || inc.By != RadarName {
		return Incident{}, false
	}
	return c.resolve(inc.ID, RadarName)
}
