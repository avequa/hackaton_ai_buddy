package mcpapi

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/avequa/ai-it-buddy/core/internal/domain"
)

// EmptyInput - инструмент без аргументов
type EmptyInput struct{}

// ServicesOutput - каталог сервисов
type ServicesOutput struct {
	Services []domain.ServiceView `json:"services"`
}

// MyAccessOutput - текущие доступы и открытые заявки
type MyAccessOutput struct {
	Grants       []domain.Grant `json:"grants"`
	OpenRequests []RequestBrief `json:"open_requests"`
}

// DraftAccessInput - аргументы draft_access_request
type DraftAccessInput struct {
	Service       string `json:"service" jsonschema:"service id from list_services, e.g. gitlab"`
	Project       string `json:"project" jsonschema:"project id inside the service, e.g. payments-api"`
	Level         string `json:"level" jsonschema:"minimal level needed for the task: read, write or admin"`
	Justification string `json:"justification" jsonschema:"one sentence: which task needs this access"`
}

// DraftOutput - результат создания черновика любой заявки
type DraftOutput struct {
	Outcome      string            `json:"outcome"`
	Request      *RequestBrief     `json:"request,omitempty"`
	CurrentLevel string            `json:"current_level,omitempty"`
	Incident     *domain.Incident  `json:"incident,omitempty"`
	Alternatives []domain.ItemView `json:"alternatives,omitempty"`
	Hint         string            `json:"hint,omitempty"`
	NextStep     string            `json:"next_step,omitempty"`
}

// confirmHint - что сказать пользователю после черновика
const confirmHint = "Draft created, NOT submitted. Ask the user to check it and press Confirm on the card. " +
	"If policy.verdict is needs_mentor, explain the reasons and recommend discussing with the mentor first, " +
	"but the user may still submit."

// addAccess - инструменты Access Service
func (a *API) addAccess(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_services",
		Description: "Corporate services catalog: what each service is for, typical tasks, available access levels and " +
			"projects with the user's current access. Use it to map a task description to the services that need access.",
		Annotations: readOnly,
	}, call(a, "access", "list_services", func(_ context.Context, user string, _ EmptyInput) (ServicesOutput, string, error) {
		svcs, err := a.core.Services(user)
		return ServicesOutput{Services: svcs}, "ok", err
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_my_access",
		Description: "Access already granted to the user and access requests that are still open.",
		Annotations: readOnly,
	}, call(a, "access", "list_my_access", func(_ context.Context, user string, _ EmptyInput) (MyAccessOutput, string, error) {
		grants, open, err := a.core.MyAccess(user)
		out := MyAccessOutput{Grants: grants, OpenRequests: []RequestBrief{}}
		for _, r := range open {
			out.OpenRequests = append(out.OpenRequests, brief(r))
		}
		return out, "ok", err
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name: "draft_access_request",
		Description: "Creates a DRAFT access request (one service + project + level). The server checks duplicates and the " +
			"role policy. It does not submit: the user confirms in the UI. outcome already_granted or already_requested " +
			"means nothing new was created.",
		Annotations: draftOnly,
	}, call(a, "access", "draft_access_request", func(_ context.Context, user string, in DraftAccessInput) (DraftOutput, string, error) {
		res, err := a.core.DraftAccess(user, domain.AccessDetails(in))
		if err != nil {
			return DraftOutput{}, "", err
		}
		out := DraftOutput{Outcome: res.Outcome, CurrentLevel: res.Level}
		a.core.TrackTopic(user, "Доступ: "+in.Service)
		if res.Request != nil {
			b := brief(*res.Request)
			out.Request = &b
			if res.Outcome == "draft_created" {
				out.NextStep = confirmHint
				a.metrics.Policy.WithLabelValues(domain.KindAccess, res.Request.Policy.Verdict).Inc()
			}
		}
		return out, res.Outcome, nil
	}))
	a.addListRequests(s, "access", domain.KindAccess)
}

// HealthInput - аргументы check_service_health
type HealthInput struct {
	Service string `json:"service" jsonschema:"service id: vpn, gitlab, grafana, jira, confluence"`
}

// KBInput - аргументы search_kb
type KBInput struct {
	Query string `json:"query" jsonschema:"what the user is struggling with, in their words, e.g. vpn timeout"`
}

// KBOutput - найденные инструкции
type KBOutput struct {
	Docs []domain.DocHit `json:"docs"`
	Note string          `json:"note,omitempty"`
}

// TicketInput - аргументы draft_ticket
type TicketInput struct {
	Service    string   `json:"service" jsonschema:"affected service id"`
	Summary    string   `json:"summary" jsonschema:"one line problem summary"`
	Details    string   `json:"details,omitempty" jsonschema:"OS, client version, exact error text, when it happened"`
	StepsTried []string `json:"steps_tried,omitempty" jsonschema:"troubleshooting steps the user already tried"`
}

// addHelpdesk - инструменты Helpdesk
func (a *API) addHelpdesk(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "check_service_health",
		Description: "Is there a known incident for the service right now. Call it before troubleshooting and before a ticket.",
		Annotations: readOnly,
	}, call(a, "helpdesk", "check_service_health", func(_ context.Context, _ string, in HealthInput) (domain.Health, string, error) {
		h := a.core.ServiceHealth(in.Service)
		return h, h.Status, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_kb",
		Description: "Searches internal how-to guides (test instructions). Returns up to 3 docs with text; cite the doc title.",
		Annotations: readOnly,
	}, call(a, "helpdesk", "search_kb", func(ctx context.Context, user string, in KBInput) (KBOutput, string, error) {
		docs := a.core.SearchKB(ctx, in.Query, 3)
		out := KBOutput{Docs: docs}
		outcome := "found"
		if len(docs) > 0 {
			a.core.TrackTopic(user, docs[0].Title)
		}
		if len(docs) == 0 {
			a.core.TrackKBMiss(user)
			out.Docs = []domain.DocHit{}
			out.Note = "Nothing found. Do not invent instructions: suggest asking the mentor."
			outcome = "not_found_in_kb"
		}
		return out, outcome, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name: "draft_ticket",
		Description: "Creates a DRAFT support ticket with the troubleshooting history. If the service has a known incident, " +
			"no ticket is created (outcome known_incident). The user confirms the draft in the UI.",
		Annotations: draftOnly,
	}, call(a, "helpdesk", "draft_ticket", func(_ context.Context, user string, in TicketInput) (DraftOutput, string, error) {
		res, err := a.core.DraftTicket(user, domain.TicketDetails(in))
		if err != nil {
			return DraftOutput{}, "", err
		}
		a.core.TrackTopic(user, "Проблема: "+in.Service)
		out := DraftOutput{Outcome: res.Outcome, Incident: res.Incident}
		if res.Request != nil {
			b := brief(*res.Request)
			out.Request, out.NextStep = &b, confirmHint
		}
		return out, res.Outcome, nil
	}))
	a.addListRequests(s, "helpdesk", domain.KindHelpdesk)
}

// EquipmentInput - аргументы list_equipment
type EquipmentInput struct {
	Category string `json:"category,omitempty" jsonschema:"laptop, monitor, headset, keyboard or mouse; empty for all"`
}

// EquipmentOutput - склад и что уже у пользователя
type EquipmentOutput struct {
	Items    []domain.ItemView `json:"items"`
	Holdings []domain.Holding  `json:"my_equipment"`
}

// DraftEquipmentInput - аргументы draft_equipment_request
type DraftEquipmentInput struct {
	Action string `json:"action" jsonschema:"issue, replace, return, waitlist or purchase"`
	Item   string `json:"item" jsonschema:"item id from list_equipment"`
	Reason string `json:"reason" jsonschema:"why the user needs it, one sentence"`
}

// addInventory - инструменты Inventory
func (a *API) addInventory(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_equipment",
		Description: "Warehouse stock and the user's current equipment versus the role norm.",
		Annotations: readOnly,
	}, call(a, "inventory", "list_equipment", func(_ context.Context, user string, in EquipmentInput) (EquipmentOutput, string, error) {
		items, holdings, err := a.core.Equipment(user, in.Category)
		return EquipmentOutput{Items: items, Holdings: holdings}, "ok", err
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name: "draft_equipment_request",
		Description: "Creates a DRAFT equipment request. outcome out_of_stock returns alternatives in stock and a hint " +
			"about the waitlist or purchase. The user confirms the draft in the UI.",
		Annotations: draftOnly,
	}, call(a, "inventory", "draft_equipment_request", func(_ context.Context, user string, in DraftEquipmentInput) (DraftOutput, string, error) {
		res, err := a.core.DraftEquipment(user, domain.EquipmentDetails{Action: in.Action, Item: in.Item, Reason: in.Reason})
		if err != nil {
			return DraftOutput{}, "", err
		}
		a.core.TrackTopic(user, "Оборудование")
		out := DraftOutput{Outcome: res.Outcome, Alternatives: res.Alternatives, Hint: res.Hint}
		if res.Request != nil {
			b := brief(*res.Request)
			out.Request, out.NextStep = &b, confirmHint
			a.metrics.Policy.WithLabelValues(domain.KindEquipment, res.Request.Policy.Verdict).Inc()
		}
		return out, res.Outcome, nil
	}))
	a.addListRequests(s, "inventory", domain.KindEquipment)
}

// OnboardingOutput - путь адаптации без открытых заявок (их модель берет из списков заявок)
type OnboardingOutput struct {
	Done  int               `json:"done"`
	Total int               `json:"total"`
	Next  *domain.StepView  `json:"next,omitempty"`
	Steps []domain.StepView `json:"steps"`
}

// addOnboarding - инструменты пути адаптации
func (a *API) addOnboarding(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_onboarding",
		Description: "The intern's onboarding path by week: steps, which are done, who confirms each step and the next step.",
		Annotations: readOnly,
	}, call(a, "onboarding", "get_onboarding", func(_ context.Context, user string, _ EmptyInput) (OnboardingOutput, string, error) {
		a.core.TrackTopic(user, "План адаптации")
		ob, err := a.core.Onboarding(user)
		return OnboardingOutput{Done: ob.Done, Total: ob.Total, Next: ob.Next, Steps: ob.Steps}, "ok", err
	}))
}
