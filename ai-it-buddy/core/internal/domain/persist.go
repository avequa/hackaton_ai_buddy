package domain

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// State - все, что в ядре меняется после старта. Справочники (сотрудники, каталог, роли, шаги) берутся из YAML.
// Одна и та же структура служит и полным снимком (Restore), и пачкой изменений (TakeChanges).
type State struct {
	Requests  []Request
	Grants    []Grant
	Stock     map[string]int // позиция склада -> остаток
	Issued    []Issued       // выдачи по порядку; в пачке изменений - только новые
	StepsDone []StepDone
	Incidents []Incident
	Notes     []Notification
	Security  []SecurityEvent // только добавляются; в пачке - только новые
	Topics    []TopicStat
	Flags     []UserFlags
}

// Empty - в пачке нет изменений
func (s State) Empty() bool {
	return len(s.Requests)+len(s.Grants)+len(s.Stock)+len(s.Issued)+len(s.StepsDone)+len(s.Incidents)+
		len(s.Notes)+len(s.Security)+len(s.Topics)+len(s.Flags) == 0
}

// Size - сколько записей в пачке (для метрики)
func (s State) Size() int {
	return len(s.Requests) + len(s.Grants) + len(s.Stock) + len(s.Issued) + len(s.StepsDone) + len(s.Incidents) +
		len(s.Notes) + len(s.Security) + len(s.Topics) + len(s.Flags)
}

// StepDone - шаг адаптации отмечен
type StepDone struct {
	User  string
	Step  string
	Event Event
}

// TopicStat - с какой темой стажер приходил и сколько раз
type TopicStat struct {
	User  string
	Topic string
	Count int
}

// UserFlags - служебные отметки по пользователю
type UserFlags struct {
	User           string
	StarterOffered bool
	KBMisses       int
}

// dirtySet - ключи измененных объектов. Выдачи и события безопасности только добавляются,
// для них хватает числа уже сохраненных записей.
type dirtySet struct {
	requests     map[string]bool
	grants       map[[3]string]bool // user, service, project
	stock        map[string]bool
	steps        map[[2]string]bool // user, step
	incidents    map[string]bool
	notes        map[int64]bool
	topics       map[[2]string]bool // user, topic
	flags        map[string]bool
	savedIssued  int
	savedSecEvts int
}

func newDirtySet() dirtySet {
	return dirtySet{
		requests: map[string]bool{}, grants: map[[3]string]bool{}, stock: map[string]bool{}, steps: map[[2]string]bool{},
		incidents: map[string]bool{}, notes: map[int64]bool{}, topics: map[[2]string]bool{}, flags: map[string]bool{},
	}
}

func (d *dirtySet) merge(o dirtySet) {
	maps.Copy(d.requests, o.requests)
	maps.Copy(d.grants, o.grants)
	maps.Copy(d.stock, o.stock)
	maps.Copy(d.steps, o.steps)
	maps.Copy(d.incidents, o.incidents)
	maps.Copy(d.notes, o.notes)
	maps.Copy(d.topics, o.topics)
	maps.Copy(d.flags, o.flags)
}

// PendingChanges - сколько изменений еще не записано (для /readyz и метрики)
func (c *Core) PendingChanges() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.dirty
	return len(d.requests) + len(d.grants) + len(d.stock) + len(d.steps) + len(d.incidents) + len(d.notes) +
		len(d.topics) + len(d.flags) + len(c.issued) - d.savedIssued + len(c.security) - d.savedSecEvts
}

// TakeChanges - забирает накопленные изменения для записи. commit(true) - записано,
// commit(false) - запись не удалась, изменения вернутся в следующую пачку. Объекты копируются,
// поэтому запись идет без блокировки ядра.
func (c *Core) TakeChanges() (State, func(ok bool)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	taken := c.dirty
	c.dirty = newDirtySet()
	c.dirty.savedIssued, c.dirty.savedSecEvts = taken.savedIssued, taken.savedSecEvts
	issuedTo, secTo := len(c.issued), len(c.security)

	var ch State
	for _, id := range slices.Sorted(maps.Keys(taken.requests)) {
		ch.Requests = append(ch.Requests, cloneRequest(c.requests[id]))
	}
	for k := range taken.grants {
		if lvl := c.grantLevel(k[0], k[1], k[2]); lvl != "" {
			ch.Grants = append(ch.Grants, Grant{User: k[0], Service: k[1], Project: k[2], Level: lvl})
		}
	}
	for id := range taken.stock {
		if ch.Stock == nil {
			ch.Stock = map[string]int{}
		}
		ch.Stock[id] = c.items[id].Stock
	}
	ch.Issued = slices.Clone(c.issued[taken.savedIssued:issuedTo])
	for k := range taken.steps {
		ch.StepsDone = append(ch.StepsDone, StepDone{User: k[0], Step: k[1], Event: c.stepsDone[k[0]][k[1]]})
	}
	for _, inc := range c.incidentLog {
		if taken.incidents[inc.ID] {
			ch.Incidents = append(ch.Incidents, *inc)
		}
	}
	for _, n := range c.notes {
		if taken.notes[n.ID] {
			ch.Notes = append(ch.Notes, n)
		}
	}
	ch.Security = slices.Clone(c.security[taken.savedSecEvts:secTo])
	for k := range taken.topics {
		ch.Topics = append(ch.Topics, TopicStat{User: k[0], Topic: k[1], Count: c.activity[k[0]][k[1]]})
	}
	for u := range taken.flags {
		ch.Flags = append(ch.Flags, UserFlags{User: u, StarterOffered: c.starter[u], KBMisses: c.kbMisses[u]})
	}

	commit := func(ok bool) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if ok {
			c.dirty.savedIssued = max(c.dirty.savedIssued, issuedTo)
			c.dirty.savedSecEvts = max(c.dirty.savedSecEvts, secTo)
			return
		}
		c.dirty.merge(taken)
	}
	return ch, commit
}

func cloneRequest(r *Request) Request {
	cp := *r
	cp.History = slices.Clone(r.History)
	cp.Policy.Reasons = slices.Clone(r.Policy.Reasons)
	if r.Access != nil {
		a := *r.Access
		cp.Access = &a
	}
	if r.Ticket != nil {
		t := *r.Ticket
		t.StepsTried = slices.Clone(r.Ticket.StepsTried)
		cp.Ticket = &t
	}
	if r.Equipment != nil {
		e := *r.Equipment
		cp.Equipment = &e
	}
	return cp
}

// MarkAllDirty - первый запуск на пустой базе: записать начальное состояние из YAML (выданные доступы, склад, выдачи)
func (c *Core) MarkAllDirty() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, g := range c.grants {
		c.dirty.grants[[3]string{g.User, g.Service, g.Project}] = true
	}
	for id := range c.items {
		c.dirty.stock[id] = true
	}
	for _, inc := range c.incidentLog {
		c.dirty.incidents[inc.ID] = true
	}
	c.dirty.savedIssued = 0
}

// Restore - заменить рабочее состояние сохраненным (при старте). Справочники остаются из YAML;
// записи о неизвестных сотрудниках, позициях и шагах пропускаются (их убрали из справочников).
func (c *Core) Restore(st State) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.requests = map[string]*Request{}
	for i := range st.Requests {
		r := cloneRequest(&st.Requests[i])
		c.requests[r.ID] = &r
		prefix, num, _ := strings.Cut(r.ID, "-")
		for kind, p := range idPrefix {
			if n, err := strconv.Atoi(num); err == nil && p == prefix {
				c.seq[kind] = max(c.seq[kind], n)
			}
		}
	}

	c.grants = nil
	for _, g := range st.Grants {
		if _, ok := c.users[g.User]; ok {
			c.grants = append(c.grants, g)
		}
	}
	for id, qty := range st.Stock {
		if it, ok := c.items[id]; ok {
			it.Stock = qty
		}
	}
	c.issued = nil
	for _, is := range st.Issued {
		if _, ok := c.items[is.Item]; ok {
			c.issued = append(c.issued, is)
		}
	}

	c.stepsDone = map[string]map[string]Event{}
	for _, sd := range st.StepsDone {
		if c.stepsDone[sd.User] == nil {
			c.stepsDone[sd.User] = map[string]Event{}
		}
		c.stepsDone[sd.User][sd.Step] = sd.Event
	}

	incs := slices.Clone(st.Incidents)
	slices.SortFunc(incs, func(a, b Incident) int { return cmp.Compare(incidentNum(a.ID), incidentNum(b.ID)) })
	c.incidentLog, c.incidents = nil, map[string]Incident{}
	for i := range incs {
		inc := incs[i]
		c.incidentLog = append(c.incidentLog, &inc)
		if inc.ResolvedAt == nil {
			c.incidents[inc.Service] = inc
		}
	}

	c.notes = slices.Clone(st.Notes)
	slices.SortFunc(c.notes, func(a, b Notification) int { return cmp.Compare(a.ID, b.ID) })
	c.noteSeq = 0
	if len(c.notes) > 0 {
		c.noteSeq = c.notes[len(c.notes)-1].ID
	}

	c.security = slices.Clone(st.Security)
	slices.SortFunc(c.security, func(a, b SecurityEvent) int { return cmp.Compare(a.ID, b.ID) })

	c.activity, c.kbMisses, c.starter = map[string]map[string]int{}, map[string]int{}, map[string]bool{}
	for _, t := range st.Topics {
		if c.activity[t.User] == nil {
			c.activity[t.User] = map[string]int{}
		}
		c.activity[t.User][t.Topic] = t.Count
	}
	for _, f := range st.Flags {
		c.kbMisses[f.User] = f.KBMisses
		c.starter[f.User] = f.StarterOffered
	}

	c.dirty = newDirtySet()
	c.dirty.savedIssued, c.dirty.savedSecEvts = len(c.issued), len(c.security)
}

func incidentNum(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "INC-"))
	return n
}

// KBSearcher - поиск по базе знаний во внешнем хранилище (Postgres, полнотекстовый с русской морфологией)
type KBSearcher interface {
	SearchKB(ctx context.Context, query string, limit int) ([]DocHit, error)
}

// SetKB - подключить поиск в хранилище. При ошибке хранилища поиск выполняется в памяти.
func (c *Core) SetKB(kb KBSearcher) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kb = kb
}

// Docs - документы базы знаний (для синхронизации с хранилищем при старте)
func (c *Core) Docs() []Doc {
	return slices.Clone(c.docs)
}
