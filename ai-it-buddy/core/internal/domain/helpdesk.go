package domain

import (
	"fmt"
	"strings"
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

// SetIncident - включает или снимает инцидент сервиса (демо-режим вместо мониторинга)
func (c *Core) SetIncident(service, title string, active bool) Health {
	c.mu.Lock()
	if active {
		c.incidents[service] = Incident{ID: fmt.Sprintf("INC-%s-%d", strings.ToUpper(service), c.opts.Now().Unix()%10000), Service: service, Title: title, Since: c.opts.Now()}
	} else {
		delete(c.incidents, service)
	}
	c.mu.Unlock()
	return c.ServiceHealth(service)
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
