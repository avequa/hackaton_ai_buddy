// Package domain - предметная область AI IT-Buddy: сотрудники, доступы, заявки, склад, база знаний и путь адаптации.
// Права проверяются здесь, на сервере: агент только вызывает инструменты от имени пользователя.
package domain

import (
	"errors"
	"time"
)

// Ошибки домена; транспортный слой (MCP, HTTP) превращает их в понятные ответы
var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
	ErrInvalid   = errors.New("invalid argument")
	ErrConflict  = errors.New("conflict")
)

// ErrForeign - объект существует, но принадлежит другому пользователю. Для клиента это обычный not found
// (не раскрываем, что объект есть), а сервер записывает попытку как событие безопасности.
var ErrForeign error = foreignError{}

type foreignError struct{}

func (foreignError) Error() string        { return "not found" }
func (foreignError) Is(target error) bool { return target == ErrNotFound }

// Роли сотрудников
const (
	RoleIntern = "intern"
	RoleMentor = "mentor"
	RoleLead   = "lead"
	RoleSRE    = "sre"
)

// Уровни доступа по возрастанию
const (
	LevelRead  = "read"
	LevelWrite = "write"
	LevelAdmin = "admin"
)

// levelRank - порядок уровней, чтобы понять, покрывает ли выданный доступ запрошенный
var levelRank = map[string]int{LevelRead: 1, LevelWrite: 2, LevelAdmin: 3}

// Кто согласует уровень доступа (значения approvers в каталоге)
const (
	ApproverAuto   = "auto"
	ApproverOwner  = "owner"
	ApproverLead   = "lead"
	ApproverDevOps = "devops"
)

// Виды заявок
const (
	KindAccess    = "access"
	KindHelpdesk  = "helpdesk"
	KindEquipment = "equipment"
)

// Статусы заявки. draft создает агент, дальше заявку двигает только человек (подтверждение) или система согласования.
const (
	StatusDraft         = "draft"          // черновик, ждет подтверждения пользователем
	StatusPendingMentor = "pending_mentor" // не по роли: сначала решение наставника
	StatusInReview      = "in_review"      // у согласующего (владелец, руководитель, DevOps)
	StatusApproved      = "approved"       // доступ выдан / оборудование зарезервировано
	StatusRejected      = "rejected"
	StatusNew           = "new"         // обращение в поддержку зарегистрировано
	StatusInProgress    = "in_progress" // обращение взяли в работу
	StatusReady         = "ready"       // оборудование готово к выдаче
	StatusWaitlisted    = "waitlisted"  // в листе ожидания
	StatusCancelled     = "cancelled"
)

// finalStatuses - заявка закрыта и больше не меняется
var finalStatuses = map[string]bool{StatusApproved: true, StatusRejected: true, StatusReady: true, StatusCancelled: true}

// Действия с оборудованием
const (
	ActionIssue    = "issue"
	ActionReplace  = "replace"
	ActionReturn   = "return"
	ActionWaitlist = "waitlist"
	ActionPurchase = "purchase"
)

// Подтверждение шага адаптации
const (
	ConfirmSelf   = "self"
	ConfirmMentor = "mentor"
	ConfirmLead   = "lead"
	ConfirmAuto   = "auto"
)

// Policy verdicts
const (
	VerdictOK          = "ok"
	VerdictNeedsMentor = "needs_mentor"
)

// User - сотрудник; в Авито берется из системы учета сотрудников и IdP
type User struct {
	ID        string `yaml:"id" json:"id"`
	Name      string `yaml:"name" json:"name"`
	Role      string `yaml:"role" json:"role"`
	Position  string `yaml:"position" json:"position"`
	Grade     string `yaml:"grade" json:"grade"`
	Team      string `yaml:"team" json:"team"`
	Mentor    string `yaml:"mentor" json:"mentor,omitempty"`
	Lead      string `yaml:"lead" json:"lead,omitempty"`
	StartDate string `yaml:"start_date" json:"start_date,omitempty"`
}

// Project - проект внутри сервиса (репозиторий, пространство, доска)
type Project struct {
	ID    string `yaml:"id" json:"id"`
	Name  string `yaml:"name" json:"name"`
	Team  string `yaml:"team" json:"team"` // "*" - проект общий для всех команд
	Owner string `yaml:"owner" json:"owner"`
}

// Service - корпоративный сервис из каталога
type Service struct {
	ID           string            `yaml:"id" json:"id"`
	Name         string            `yaml:"name" json:"name"`
	Description  string            `yaml:"description" json:"description"`
	TypicalTasks []string          `yaml:"typical_tasks" json:"typical_tasks"`
	Approvers    map[string]string `yaml:"approvers" json:"approvers"`
	Projects     []Project         `yaml:"projects" json:"projects"`
}

// Grant - выданный доступ
type Grant struct {
	User    string `yaml:"user" json:"-"`
	Service string `yaml:"service" json:"service"`
	Project string `yaml:"project" json:"project"`
	Level   string `yaml:"level" json:"level"`
}

// RoleMatch - к кому относится профиль роли; пустое поле совпадает с любым значением
type RoleMatch struct {
	Role     string `yaml:"role"`
	Position string `yaml:"position"`
}

// RoleProfile - типичные доступы и норма оборудования для роли
type RoleProfile struct {
	Match         RoleMatch           `yaml:"match"`
	TypicalAccess map[string][]string `yaml:"typical_access"`
	EquipmentNorm map[string]int      `yaml:"equipment_norm"`
}

// Item - позиция на складе
type Item struct {
	ID       string `yaml:"id" json:"id"`
	Category string `yaml:"category" json:"category"`
	Model    string `yaml:"model" json:"model"`
	Specs    string `yaml:"specs" json:"specs,omitempty"`
	Stock    int    `yaml:"stock" json:"stock"`
}

// Issued - оборудование, выданное сотруднику
type Issued struct {
	User     string `yaml:"user"`
	Item     string `yaml:"item"`
	Returned bool   `yaml:"-"` // событие возврата: журнал выдач только дописывается
}

// StepAuto - событие, по которому шаг адаптации закрывается сам
type StepAuto struct {
	Service string `yaml:"service" json:"service"`
}

// Step - шаг пути адаптации
type Step struct {
	ID      string    `yaml:"id" json:"id"`
	Week    int       `yaml:"week" json:"week"`
	Title   string    `yaml:"title" json:"title"`
	Confirm string    `yaml:"confirm" json:"confirm"`
	Auto    *StepAuto `yaml:"auto" json:"auto,omitempty"`
}

// Doc - документ базы знаний (тестовая инструкция)
type Doc struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Service string   `json:"service"`
	Tags    []string `json:"tags"`
	Body    string   `json:"-"`
}

// Incident - известный сбой сервиса
type Incident struct {
	ID         string     `json:"id"`
	Service    string     `json:"service"`
	Title      string     `json:"title"`
	Message    string     `json:"message,omitempty"` // что делать пользователям, например "работайте через резервный шлюз"
	Since      time.Time  `json:"since"`
	By         string     `json:"by,omitempty"` // кто объявил: SRE, руководитель или мониторинг
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

// Seed - начальные данные
type Seed struct {
	Users     []User
	Grants    []Grant
	Services  []Service
	Roles     []RoleProfile
	Items     []Item
	Issued    []Issued
	Steps     []Step
	Docs      []Doc
	Incidents []Incident
}

// Policy - решение сервера о заявке: можно ли сразу к согласующему или сначала к наставнику
type Policy struct {
	Verdict string   `json:"verdict"`
	Reasons []string `json:"reasons,omitempty"`
}

// Event - запись истории заявки
type Event struct {
	At     time.Time `json:"at"`
	Status string    `json:"status"`
	By     string    `json:"by"`
	Note   string    `json:"note,omitempty"`
}

// AccessDetails - что именно запрошено в заявке на доступ
type AccessDetails struct {
	Service       string `json:"service"`
	Project       string `json:"project"`
	Level         string `json:"level"`
	Justification string `json:"justification"`
}

// TicketDetails - обращение в техподдержку с историей диагностики
type TicketDetails struct {
	Service    string   `json:"service"`
	Summary    string   `json:"summary"`
	Details    string   `json:"details,omitempty"`
	StepsTried []string `json:"steps_tried,omitempty"`
}

// EquipmentDetails - заявка на оборудование
type EquipmentDetails struct {
	Action string `json:"action"`
	Item   string `json:"item"`
	Model  string `json:"model"`
	Reason string `json:"reason"`
}

// Request - заявка любого вида; результат действия, который сохраняется и показывается пользователю
type Request struct {
	ID           string            `json:"id"`
	Kind         string            `json:"kind"`
	User         string            `json:"user"`
	Status       string            `json:"status"`
	Title        string            `json:"title"`
	Policy       Policy            `json:"policy"`
	Approver     string            `json:"approver,omitempty"`
	ApproverName string            `json:"approver_name,omitempty"`
	Resolution   string            `json:"resolution,omitempty"` // причина отказа или итог
	Access       *AccessDetails    `json:"access,omitempty"`
	Ticket       *TicketDetails    `json:"ticket,omitempty"`
	Equipment    *EquipmentDetails `json:"equipment,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	History      []Event           `json:"history"`
	DueAt        time.Time         `json:"-"` // когда система согласования сделает следующий шаг; хранится отдельной колонкой
}

// Final - заявка закрыта
func (r *Request) Final() bool { return finalStatuses[r.Status] }
