package domain

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// ItemView - позиция склада глазами пользователя
type ItemView struct {
	Item
	InStock bool `json:"in_stock"`
}

// Holding - сколько оборудования категории у пользователя и сколько положено по роли
type Holding struct {
	Category string   `json:"category"`
	Has      int      `json:"has"`
	Norm     int      `json:"norm"`
	Models   []string `json:"models,omitempty"`
}

// Equipment - склад (с фильтром по категории) и что уже выдано пользователю
func (c *Core) Equipment(userID, category string) ([]ItemView, []Holding, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	u, err := c.user(userID)
	if err != nil {
		return nil, nil, err
	}
	var items []ItemView
	for _, it := range c.items {
		if category == "" || it.Category == category {
			items = append(items, ItemView{Item: *it, InStock: it.Stock > 0})
		}
	}
	slices.SortFunc(items, func(a, b ItemView) int {
		return cmp.Or(cmp.Compare(a.Category, b.Category), cmp.Compare(a.ID, b.ID))
	})

	norm := c.profile(u).EquipmentNorm
	byCat := map[string]*Holding{}
	for cat, n := range norm {
		byCat[cat] = &Holding{Category: cat, Norm: n}
	}
	for _, is := range c.issued {
		if is.User != u.ID {
			continue
		}
		it := c.items[is.Item]
		h, ok := byCat[it.Category]
		if !ok {
			h = &Holding{Category: it.Category}
			byCat[it.Category] = h
		}
		h.Has++
		h.Models = append(h.Models, it.Model)
	}
	holdings := make([]Holding, 0, len(byCat))
	for _, h := range byCat {
		if category == "" || h.Category == category {
			holdings = append(holdings, *h)
		}
	}
	slices.SortFunc(holdings, func(a, b Holding) int { return cmp.Compare(a.Category, b.Category) })
	return items, holdings, nil
}

// EquipmentDraft - результат попытки создать заявку на оборудование
type EquipmentDraft struct {
	Outcome      string     `json:"outcome"` // draft_created | out_of_stock
	Request      *Request   `json:"request,omitempty"`
	Alternatives []ItemView `json:"alternatives,omitempty"`
	Hint         string     `json:"hint,omitempty"`
}

// countIssued - сколько оборудования категории уже у пользователя
func (c *Core) countIssued(userID, category string) int {
	n := 0
	for _, is := range c.issued {
		if is.User == userID && c.items[is.Item].Category == category {
			n++
		}
	}
	return n
}

// DraftEquipment - черновик заявки на выдачу, замену, возврат, лист ожидания или закупку.
// Нет на складе - сервер предлагает альтернативы и лист ожидания; сверх нормы роли - сначала к наставнику.
func (c *Core) DraftEquipment(userID string, e EquipmentDetails) (EquipmentDraft, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	u, err := c.user(userID)
	if err != nil {
		return EquipmentDraft{}, err
	}
	it, ok := c.items[e.Item]
	if !ok {
		return EquipmentDraft{}, fmt.Errorf("unknown item %q, call list_equipment first: %w", e.Item, ErrInvalid)
	}
	e.Reason = strings.TrimSpace(e.Reason)
	if e.Reason == "" {
		return EquipmentDraft{}, fmt.Errorf("reason is required: %w", ErrInvalid)
	}
	e.Model = it.Model
	norm := c.profile(u).EquipmentNorm[it.Category]
	has := c.countIssued(u.ID, it.Category)

	policy := Policy{Verdict: VerdictOK}
	switch e.Action {
	case ActionIssue, ActionPurchase, ActionWaitlist:
		if has+1 > norm {
			policy = Policy{Verdict: VerdictNeedsMentor, Reasons: []string{
				fmt.Sprintf("норма для роли %s/%s: %d шт. категории %s, у тебя уже %d", u.Role, u.Position, norm, it.Category, has),
			}}
		}
	case ActionReplace, ActionReturn:
		if has == 0 {
			return EquipmentDraft{}, fmt.Errorf("you have no %s to %s: %w", it.Category, e.Action, ErrInvalid)
		}
	default:
		return EquipmentDraft{}, fmt.Errorf("unknown action %q, use issue, replace, return, waitlist or purchase: %w", e.Action, ErrInvalid)
	}

	if (e.Action == ActionIssue || e.Action == ActionReplace) && it.Stock == 0 {
		var alts []ItemView
		for _, other := range c.items {
			if other.Category == it.Category && other.Stock > 0 {
				alts = append(alts, ItemView{Item: *other, InStock: true})
			}
		}
		slices.SortFunc(alts, func(a, b ItemView) int { return cmp.Compare(a.ID, b.ID) })
		hint := "Можно взять альтернативу из наличия или встать в лист ожидания (action=waitlist)."
		if policy.Verdict != VerdictNeedsMentor {
			hint += " Закупка (action=purchase) тоже возможна: позиция входит в норму роли."
		}
		return EquipmentDraft{Outcome: "out_of_stock", Alternatives: alts, Hint: hint}, nil
	}
	if e.Action == ActionPurchase && policy.Verdict == VerdictNeedsMentor {
		return EquipmentDraft{}, fmt.Errorf("purchase is only possible within the role norm; %s: %w", policy.Reasons[0], ErrForbidden)
	}

	titles := map[string]string{
		ActionIssue: "Выдача", ActionReplace: "Замена", ActionReturn: "Возврат", ActionWaitlist: "Лист ожидания", ActionPurchase: "Закупка",
	}
	r := c.newRequest(KindEquipment, u, fmt.Sprintf("%s: %s", titles[e.Action], it.Model))
	r.Equipment = &e
	r.Policy = policy
	r.Approver, r.ApproverName = "it-warehouse", "IT-склад"
	if e.Action == ActionPurchase {
		r.Approver, r.ApproverName = u.Lead, c.name(u.Lead)
	}
	draft := *r
	return EquipmentDraft{Outcome: "draft_created", Request: &draft}, nil
}

// confirmEquipment - подтвержденная заявка на оборудование: резерв на складе, лист ожидания или закупка
func (c *Core) confirmEquipment(u User, r *Request) error {
	it := c.items[r.Equipment.Item]
	switch r.Equipment.Action {
	case ActionIssue, ActionReplace:
		if it.Stock == 0 {
			return fmt.Errorf("%s is out of stock now, choose another item or join the waitlist: %w", it.Model, ErrConflict)
		}
		it.Stock--
		if r.Equipment.Action == ActionIssue {
			c.issued = append(c.issued, Issued{User: u.ID, Item: it.ID})
		}
		c.move(r, StatusApproved, "IT-склад", "позиция зарезервирована")
		r.dueAt = c.opts.Now().Add(c.opts.ApprovalDelay)
	case ActionReturn:
		r.Resolution = "Принеси оборудование на IT-склад, 3 этаж, с 10 до 18"
		c.move(r, StatusApproved, "IT-склад", r.Resolution)
	case ActionWaitlist:
		r.Resolution = "Ты в листе ожидания, пришлем уведомление, когда позиция появится"
		c.move(r, StatusWaitlisted, "IT-склад", r.Resolution)
	case ActionPurchase:
		c.move(r, StatusInReview, u.Name, "закупка отправлена руководителю")
		r.dueAt = c.opts.Now().Add(c.opts.ApprovalDelay)
	}
	return nil
}
