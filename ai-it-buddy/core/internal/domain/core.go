package domain

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// Options - настройки имитации корпоративных систем
type Options struct {
	// ApprovalDelay - через сколько согласующий (владелец, DevOps, поддержка, склад) принимает решение
	ApprovalDelay time.Duration
	Now           func() time.Time
	// OnKBError - поиск в Postgres не удался и выполнен в памяти (для лога и метрики)
	OnKBError func(error)
}

// Core - состояние имитаций корпоративных сервисов: Access, Helpdesk, Inventory и путь адаптации.
// Рабочая копия живет в памяти, изменения помечаются и в фоне пишутся в Postgres (TakeChanges, Restore).
// Так ядро отвечает быстро и не падает, если база ненадолго недоступна.
type Core struct {
	mu        sync.Mutex
	opts      Options
	users     map[string]User
	services  map[string]Service
	roles     []RoleProfile
	grants    []Grant
	items     map[string]*Item
	issued    []Issued
	steps     []Step
	stepsDone map[string]map[string]Event // user -> step -> кто и когда подтвердил
	docs      []Doc
	incidents map[string]Incident // service -> активный инцидент
	requests  map[string]*Request
	seq       map[string]int

	incidentLog []*Incident               // все инциденты, включая закрытые
	notes       []Notification            // лента уведомлений всех пользователей
	noteSeq     int64                     //
	security    []SecurityEvent           // попытки выйти за права
	alerted     map[string]time.Time      // user|kind -> когда последний раз будили наставника (без спама)
	activity    map[string]map[string]int // user -> тема -> сколько раз (без текста переписки)
	kbMisses    map[string]int            // user -> сколько раз база знаний не нашла ответ
	starter     map[string]bool           // кому уже предложили стартовый набор

	dirty dirtySet   // что изменилось с последней записи в хранилище
	kb    KBSearcher // полнотекстовый поиск в Postgres; nil - поиск в памяти
}

// idPrefix - префикс номера заявки по виду, как в настоящих системах заявок
var idPrefix = map[string]string{KindAccess: "ACC", KindHelpdesk: "HD", KindEquipment: "INV"}

// New - собирает ядро из начальных данных
func New(seed Seed, opts Options) (*Core, error) {
	if opts.ApprovalDelay <= 0 {
		opts.ApprovalDelay = 20 * time.Second
	}
	if opts.Now == nil {
		// UTC: одинаково до и после перезапуска (Postgres отдает время в UTC)
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	c := &Core{
		opts:      opts,
		users:     map[string]User{},
		services:  map[string]Service{},
		roles:     seed.Roles,
		grants:    slices.Clone(seed.Grants),
		items:     map[string]*Item{},
		issued:    slices.Clone(seed.Issued),
		steps:     seed.Steps,
		stepsDone: map[string]map[string]Event{},
		docs:      seed.Docs,
		incidents: map[string]Incident{},
		requests:  map[string]*Request{},
		seq:       map[string]int{KindAccess: 1000, KindHelpdesk: 300, KindEquipment: 80},
		alerted:   map[string]time.Time{},
		activity:  map[string]map[string]int{},
		kbMisses:  map[string]int{},
		starter:   map[string]bool{},
		dirty:     newDirtySet(),
	}
	for _, u := range seed.Users {
		c.users[u.ID] = u
	}
	for _, s := range seed.Services {
		c.services[s.ID] = s
	}
	for i := range seed.Items {
		item := seed.Items[i]
		c.items[item.ID] = &item
	}
	for _, inc := range seed.Incidents {
		c.incidents[inc.Service] = inc
		logged := inc
		c.incidentLog = append(c.incidentLog, &logged)
	}
	// начальное состояние из YAML не считается изменением: в память ничего писать не нужно,
	// а пустую базу заполняет MarkAllDirty
	c.dirty.savedIssued = len(c.issued)
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// validate - ссылки в начальных данных указывают на существующие объекты
func (c *Core) validate() error {
	for _, u := range c.users {
		for _, ref := range []string{u.Mentor, u.Lead} {
			if _, ok := c.users[ref]; ref != "" && !ok {
				return fmt.Errorf("user %s: unknown mentor or lead %q", u.ID, ref)
			}
		}
	}
	for _, g := range c.grants {
		if _, ok := c.users[g.User]; !ok {
			return fmt.Errorf("grant: unknown user %q", g.User)
		}
		if _, _, err := c.lookupProject(g.Service, g.Project); err != nil {
			return fmt.Errorf("grant for %s: %w", g.User, err)
		}
	}
	for _, s := range c.services {
		for _, p := range s.Projects {
			if _, ok := c.users[p.Owner]; !ok {
				return fmt.Errorf("service %s project %s: unknown owner %q", s.ID, p.ID, p.Owner)
			}
		}
	}
	for _, is := range c.issued {
		if _, ok := c.items[is.Item]; !ok {
			return fmt.Errorf("issued: unknown item %q", is.Item)
		}
	}
	return nil
}

// User - сотрудник по идентификатору
func (c *Core) User(id string) (User, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.user(id)
}

func (c *Core) user(id string) (User, error) {
	u, ok := c.users[id]
	if !ok {
		return User{}, fmt.Errorf("user %q: %w", id, ErrNotFound)
	}
	return u, nil
}

// Users - все сотрудники (для экрана выбора тестовой персоны)
func (c *Core) Users() []User {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]User, 0, len(c.users))
	for _, u := range c.users {
		out = append(out, u)
	}
	slices.SortFunc(out, func(a, b User) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// name - имя сотрудника или сама ссылка (devops, auto)
func (c *Core) name(id string) string {
	switch id {
	case ApproverDevOps:
		return "DevOps"
	case ApproverAuto:
		return "автоматически"
	}
	if u, ok := c.users[id]; ok {
		return u.Name
	}
	return id
}

// supervisor - кто решает по заявкам "не по роли": наставник, а если его нет - руководитель
func (c *Core) supervisor(u User) string {
	if u.Mentor != "" {
		return u.Mentor
	}
	return u.Lead
}

// profile - профиль роли пользователя (первый подходящий)
func (c *Core) profile(u User) RoleProfile {
	for _, p := range c.roles {
		if (p.Match.Role == "" || p.Match.Role == u.Role) && (p.Match.Position == "" || p.Match.Position == u.Position) {
			return p
		}
	}
	return RoleProfile{}
}

// newRequest - создает черновик заявки с номером и первой записью истории
func (c *Core) newRequest(kind string, u User, title string) *Request {
	c.seq[kind]++
	now := c.opts.Now()
	r := &Request{
		ID:        fmt.Sprintf("%s-%d", idPrefix[kind], c.seq[kind]),
		Kind:      kind,
		User:      u.ID,
		Status:    StatusDraft,
		Title:     title,
		Policy:    Policy{Verdict: VerdictOK},
		CreatedAt: now,
		UpdatedAt: now,
	}
	r.History = []Event{{At: now, Status: StatusDraft, By: "AI IT-Buddy", Note: "черновик, ждет подтверждения"}}
	c.requests[r.ID] = r
	c.dirty.requests[r.ID] = true
	return r
}

// move - меняет статус заявки и пишет историю
func (c *Core) move(r *Request, status, by, note string) {
	now := c.opts.Now()
	r.Status = status
	r.UpdatedAt = now
	r.History = append(r.History, Event{At: now, Status: status, By: by, Note: note})
	c.dirty.requests[r.ID] = true
}

// canView - заявку видит автор, его наставник и руководитель; остальным она "не найдена", чтобы не раскрывать ее существование
func (c *Core) canView(viewer User, r *Request) bool {
	if r.User == viewer.ID {
		return true
	}
	owner := c.users[r.User]
	return owner.Mentor == viewer.ID || owner.Lead == viewer.ID
}

// Request - заявка, если пользователь имеет право ее видеть
func (c *Core) Request(viewerID, id string) (Request, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	viewer, err := c.user(viewerID)
	if err != nil {
		return Request{}, err
	}
	r, ok := c.requests[id]
	if !ok {
		return Request{}, fmt.Errorf("request %s: %w", id, ErrNotFound)
	}
	if !c.canView(viewer, r) {
		return Request{}, fmt.Errorf("request %s: %w", id, ErrForeign)
	}
	return *r, nil
}

// MyRequests - заявки пользователя, новые сверху; kind пустой - все виды
func (c *Core) MyRequests(userID, kind string) []Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Request
	for _, r := range c.requests {
		if r.User == userID && (kind == "" || r.Kind == kind) {
			out = append(out, *r)
		}
	}
	sortRequests(out)
	return out
}

// sortRequests - новые сверху, при равном времени по номеру
func sortRequests(rs []Request) {
	slices.SortFunc(rs, func(a, b Request) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(b.ID, a.ID)
	})
}

// own - заявка пользователя в нужном статусе
func (c *Core) own(userID, id, status string) (*Request, error) {
	r, ok := c.requests[id]
	if !ok {
		return nil, fmt.Errorf("request %s: %w", id, ErrNotFound)
	}
	if r.User != userID {
		return nil, fmt.Errorf("request %s: %w", id, ErrForeign)
	}
	if r.Status != status {
		return nil, fmt.Errorf("request %s is %s, expected %s: %w", id, r.Status, status, ErrConflict)
	}
	return r, nil
}

// Confirm - пользователь подтверждает черновик. Вызывается только из интерфейса (HTTP API с токеном сессии),
// у агента такого инструмента нет: заявку от имени человека нельзя отправить без его действия.
func (c *Core) Confirm(userID, id string) (Request, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.own(userID, id, StatusDraft)
	if err != nil {
		return Request{}, err
	}
	u := c.users[userID]
	now := c.opts.Now()

	if r.Policy.Verdict == VerdictNeedsMentor {
		sup := c.supervisor(u)
		r.Approver, r.ApproverName = sup, c.name(sup)
		c.move(r, StatusPendingMentor, u.Name, "не по роли, отправлено наставнику")
		c.notify(sup, NoteApproval, "Нужно ваше решение", fmt.Sprintf("%s: %s. %s", u.Name, r.Title, strings.Join(r.Policy.Reasons, "; ")), r.ID)
		return *r, nil
	}

	switch r.Kind {
	case KindAccess:
		if r.Approver == ApproverAuto {
			c.grant(u.ID, *r.Access)
			c.move(r, StatusApproved, "Access Service", "выдан автоматически")
			return *r, nil
		}
		c.move(r, StatusInReview, u.Name, "отправлено на согласование")
		r.DueAt = now.Add(c.opts.ApprovalDelay)
	case KindHelpdesk:
		c.move(r, StatusNew, u.Name, "обращение зарегистрировано")
		r.DueAt = now.Add(c.opts.ApprovalDelay)
	case KindEquipment:
		if err := c.confirmEquipment(u, r); err != nil {
			return Request{}, err
		}
	}
	return *r, nil
}

// Cancel - пользователь отменяет свою заявку, пока по ней не принято решение
func (c *Core) Cancel(userID, id string) (Request, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.requests[id]
	if !ok {
		return Request{}, fmt.Errorf("request %s: %w", id, ErrNotFound)
	}
	if r.User != userID {
		return Request{}, fmt.Errorf("request %s: %w", id, ErrForeign)
	}
	if r.Final() || r.Status == StatusInProgress {
		return Request{}, fmt.Errorf("request %s is %s and cannot be cancelled: %w", id, r.Status, ErrConflict)
	}
	c.move(r, StatusCancelled, c.users[userID].Name, "отменено пользователем")
	return *r, nil
}

// Approvals - заявки "не по роли", которые ждут решения этого наставника или руководителя
func (c *Core) Approvals(userID string) []Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Request
	for _, r := range c.requests {
		if r.Status == StatusPendingMentor && r.Approver == userID {
			out = append(out, *r)
		}
	}
	sortRequests(out)
	return out
}

// Decide - наставник одобряет или отклоняет заявку "не по роли"
func (c *Core) Decide(mentorID, id string, approve bool, comment string) (Request, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.requests[id]
	if !ok || r.Status != StatusPendingMentor {
		return Request{}, fmt.Errorf("approval %s: %w", id, ErrNotFound)
	}
	if r.Approver != mentorID {
		return Request{}, fmt.Errorf("approval %s: %w", id, ErrForeign)
	}
	mentor := c.users[mentorID]
	if !approve {
		if comment == "" {
			comment = "наставник отклонил заявку"
		}
		r.Resolution = comment
		c.move(r, StatusRejected, mentor.Name, comment)
		c.notifyStatus(r)
		return *r, nil
	}
	c.move(r, StatusInReview, mentor.Name, cmp.Or(comment, "наставник одобрил"))
	r.Approver, r.ApproverName = c.approverAfterMentor(r)
	c.notifyStatus(r)
	if r.Kind != KindEquipment {
		r.DueAt = c.opts.Now().Add(c.opts.ApprovalDelay)
		return *r, nil
	}
	if err := c.confirmEquipment(c.users[r.User], r); err != nil {
		// пока наставник думал, позицию разобрали: заявка остается у склада, пользователь видит причину
		r.Resolution = "Позиция закончилась на складе, можно выбрать другую или встать в лист ожидания"
	}
	return *r, nil
}

// approverAfterMentor - кто согласует после наставника
func (c *Core) approverAfterMentor(r *Request) (string, string) {
	switch r.Kind {
	case KindAccess:
		id := c.accessApprover(c.users[r.User], *r.Access)
		if id == ApproverAuto {
			id = ApproverOwner
		}
		return id, c.name(id)
	case KindEquipment:
		return "it-warehouse", "IT-склад"
	}
	return "it-support", "IT-поддержка"
}

// Tick - имитация работы согласующих: заявки, у которых подошло время, двигаются дальше.
// Вызывается раз в секунду; в тестах - вручную со сдвинутыми часами.
func (c *Core) Tick() []Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.opts.Now()
	var changed []Request
	for _, r := range c.requests {
		if r.DueAt.IsZero() || now.Before(r.DueAt) {
			continue
		}
		r.DueAt = time.Time{}
		c.dirty.requests[r.ID] = true
		switch {
		case r.Kind == KindAccess && r.Status == StatusInReview:
			c.decideAccess(r)
		case r.Kind == KindHelpdesk && r.Status == StatusNew:
			r.ApproverName = "Дежурный IT-поддержки"
			c.move(r, StatusInProgress, "IT-поддержка", "обращение взяли в работу")
		case r.Kind == KindEquipment && r.Status == StatusApproved:
			r.Resolution = "Готово к выдаче: IT-склад, 3 этаж, с 10 до 18"
			c.move(r, StatusReady, "IT-склад", r.Resolution)
		case r.Kind == KindEquipment && r.Status == StatusInReview:
			r.Resolution = "Закупка согласована, ориентировочный срок поставки - 2 недели"
			c.move(r, StatusApproved, "Руководитель", r.Resolution)
		default:
			continue
		}
		c.notifyStatus(r)
		changed = append(changed, *r)
	}
	return changed
}
