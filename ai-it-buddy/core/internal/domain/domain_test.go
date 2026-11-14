package domain_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/avequa/ai-it-buddy/core/internal/domain"
	"github.com/avequa/ai-it-buddy/core/internal/seed"
)

// clock - управляемые часы для имитации согласующих
type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock               { return &clock{t: time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)} }

// must - значение без ошибки; паника в тесте валит тест с понятным сообщением
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// newCore - ядро на настоящих синтетических данных из data/
func newCore(t *testing.T) (*domain.Core, *clock) {
	t.Helper()
	s, err := seed.Load("../../../data")
	if err != nil {
		t.Fatal(err)
	}
	clk := newClock()
	c, err := domain.New(s, domain.Options{ApprovalDelay: 10 * time.Second, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	return c, clk
}

// tick - сдвигает время и запускает согласующих
func tick(c *domain.Core, clk *clock) []domain.Request {
	clk.add(11 * time.Second)
	return c.Tick()
}

func TestAccess_OwnerApproves(t *testing.T) {
	c, clk := newCore(t)
	d := must(c.DraftAccess("anna.intern", domain.AccessDetails{Service: "grafana", Project: "payments-dashboards", Level: "write", Justification: "поправить алерт"}))
	if d.Outcome != "draft_created" || d.Request.Policy.Verdict != domain.VerdictOK || d.Request.Approver != "igor.mentor" {
		t.Fatalf("draft = %+v", d)
	}
	r := must(c.Confirm("anna.intern", d.Request.ID))
	if r.Status != domain.StatusInReview {
		t.Fatalf("status = %s", r.Status)
	}
	if changed := c.Tick(); len(changed) != 0 {
		t.Fatalf("decided too early: %v", changed)
	}
	changed := tick(c, clk)
	if len(changed) != 1 || changed[0].Status != domain.StatusApproved {
		t.Fatalf("changed = %+v", changed)
	}
	again := must(c.DraftAccess("anna.intern", domain.AccessDetails{Service: "grafana", Project: "payments-dashboards", Level: "read", Justification: "x"}))
	if again.Outcome != "already_granted" || again.Level != "write" {
		t.Fatalf("again = %+v", again)
	}
	ob := must(c.Onboarding("anna.intern"))
	for _, s := range ob.Steps {
		if s.ID == "grafana-access" && !s.Done {
			t.Fatal("auto step grafana-access must be done after grant")
		}
	}
}

func TestAccess_AutoReadAndDuplicate(t *testing.T) {
	c, _ := newCore(t)
	d := must(c.DraftAccess("anna.intern", domain.AccessDetails{Service: "gitlab", Project: "payments-api", Level: "read", Justification: "читать код"}))
	dup := must(c.DraftAccess("anna.intern", domain.AccessDetails{Service: "gitlab", Project: "payments-api", Level: "write", Justification: "другое"}))
	if dup.Outcome != "already_requested" || dup.Request.ID != d.Request.ID {
		t.Fatalf("dup = %+v", dup)
	}
	r := must(c.Confirm("anna.intern", d.Request.ID))
	if r.Status != domain.StatusApproved {
		t.Fatalf("auto level must be approved at once, got %s", r.Status)
	}
	grants, open := func() ([]domain.Grant, []domain.Request) {
		g, o, err := c.MyAccess("anna.intern")
		if err != nil {
			t.Fatal(err)
		}
		return g, o
	}()
	if len(open) != 0 || len(grants) != 3 {
		t.Fatalf("grants=%v open=%v", grants, open)
	}
}

func TestAccess_NotByRoleGoesToMentorThenRejected(t *testing.T) {
	c, clk := newCore(t)
	d := must(c.DraftAccess("anna.intern", domain.AccessDetails{Service: "k8s-prod", Project: "prod-cluster", Level: "read", Justification: "посмотреть поды"}))
	if d.Request.Policy.Verdict != domain.VerdictNeedsMentor || len(d.Request.Policy.Reasons) == 0 {
		t.Fatalf("policy = %+v", d.Request.Policy)
	}
	r := must(c.Confirm("anna.intern", d.Request.ID))
	if r.Status != domain.StatusPendingMentor || r.Approver != "igor.mentor" {
		t.Fatalf("r = %+v", r)
	}
	if got := c.Approvals("igor.mentor"); len(got) != 1 {
		t.Fatalf("approvals = %v", got)
	}
	if _, err := c.Decide("maria.mentor", r.ID, true, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("other mentor decided: %v", err)
	}
	r = must(c.Decide("igor.mentor", r.ID, true, "ок, для разбора инцидента"))
	if r.Status != domain.StatusInReview {
		t.Fatalf("status = %s", r.Status)
	}
	changed := tick(c, clk)
	if len(changed) != 1 || changed[0].Status != domain.StatusRejected || !strings.Contains(changed[0].Resolution, "дежурных") {
		t.Fatalf("changed = %+v", changed)
	}
}

func TestAccess_MentorRejectsAndOtherTeam(t *testing.T) {
	c, _ := newCore(t)
	d := must(c.DraftAccess("anna.intern", domain.AccessDetails{Service: "gitlab", Project: "cabinet-web", Level: "write", Justification: "помочь соседям"}))
	if d.Request.Policy.Verdict != domain.VerdictNeedsMentor || !strings.Contains(d.Request.Policy.Reasons[0], "cabinet") {
		t.Fatalf("policy = %+v", d.Request.Policy)
	}
	r := must(c.Confirm("anna.intern", d.Request.ID))
	r = must(c.Decide("igor.mentor", r.ID, false, ""))
	if r.Status != domain.StatusRejected || r.Resolution == "" {
		t.Fatalf("r = %+v", r)
	}
}

func TestAccess_AdminRejectedAfterMentor(t *testing.T) {
	c, clk := newCore(t)
	d := must(c.DraftAccess("anna.intern", domain.AccessDetails{Service: "jira", Project: "PAY", Level: "admin", Justification: "настроить доску"}))
	r := must(c.Confirm("anna.intern", d.Request.ID))
	must(c.Decide("igor.mentor", r.ID, true, ""))
	changed := tick(c, clk)
	if len(changed) != 1 || !strings.Contains(changed[0].Resolution, "write") {
		t.Fatalf("changed = %+v", changed)
	}
}

func TestAccess_Errors(t *testing.T) {
	c, _ := newCore(t)
	cases := []domain.AccessDetails{
		{Service: "nope", Project: "x", Level: "read", Justification: "x"},
		{Service: "gitlab", Project: "nope", Level: "read", Justification: "x"},
		{Service: "vpn", Project: "corp", Level: "write", Justification: "x"},
		{Service: "gitlab", Project: "payments-api", Level: "read", Justification: "  "},
	}
	for _, a := range cases {
		if _, err := c.DraftAccess("anna.intern", a); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%+v: err = %v", a, err)
		}
	}
	if _, err := c.DraftAccess("ghost", cases[0]); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ghost: %v", err)
	}
	if _, err := c.Services("ghost"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("services ghost: %v", err)
	}
	if _, _, err := c.MyAccess("ghost"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("my access ghost: %v", err)
	}
	svcs := must(c.Services("anna.intern"))
	if len(svcs) != 6 || svcs[0].ID != "confluence" || svcs[0].Levels[0] != "read" {
		t.Fatalf("services = %+v", svcs[0])
	}
}

func TestRequests_VisibilityConfirmCancel(t *testing.T) {
	c, _ := newCore(t)
	d := must(c.DraftAccess("anna.intern", domain.AccessDetails{Service: "jira", Project: "PAY", Level: "write", Justification: "задачи"}))
	id := d.Request.ID

	if _, err := c.Request("pavel.intern", id); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("other intern sees request: %v", err)
	}
	for _, viewer := range []string{"anna.intern", "igor.mentor", "olga.lead"} {
		if _, err := c.Request(viewer, id); err != nil {
			t.Fatalf("%s: %v", viewer, err)
		}
	}
	if _, err := c.Request("ghost", id); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("ghost viewer")
	}
	if _, err := c.Confirm("pavel.intern", id); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("other user confirmed: %v", err)
	}
	if _, err := c.Cancel("pavel.intern", id); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("other user cancelled: %v", err)
	}
	r := must(c.Cancel("anna.intern", id))
	if r.Status != domain.StatusCancelled || len(r.History) != 2 {
		t.Fatalf("r = %+v", r)
	}
	if _, err := c.Confirm("anna.intern", id); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("confirm cancelled: %v", err)
	}
	if _, err := c.Cancel("anna.intern", id); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("cancel twice: %v", err)
	}
	if got := c.MyRequests("anna.intern", domain.KindAccess); len(got) != 1 {
		t.Fatalf("my requests = %v", got)
	}
}

func TestHelpdesk_IncidentAndTicket(t *testing.T) {
	c, clk := newCore(t)
	if h := c.ServiceHealth("vpn"); h.Status != "ok" {
		t.Fatalf("health = %+v", h)
	}
	h := c.SetIncident("vpn", "VPN недоступен", true)
	if h.Status != "incident" || h.Incident.Title != "VPN недоступен" {
		t.Fatalf("health = %+v", h)
	}
	d := must(c.DraftTicket("anna.intern", domain.TicketDetails{Service: "vpn", Summary: "не подключается VPN"}))
	if d.Outcome != "known_incident" || d.Request != nil {
		t.Fatalf("ticket during incident = %+v", d)
	}
	c.SetIncident("vpn", "", false)
	d = must(c.DraftTicket("anna.intern", domain.TicketDetails{Service: "vpn", Summary: "не подключается VPN", StepsTried: []string{"перезапуск"}}))
	if d.Outcome != "draft_created" || d.Request.Ticket.StepsTried[0] != "перезапуск" {
		t.Fatalf("d = %+v", d)
	}
	r := must(c.Confirm("anna.intern", d.Request.ID))
	if r.Status != domain.StatusNew {
		t.Fatalf("status = %s", r.Status)
	}
	changed := tick(c, clk)
	if len(changed) != 1 || changed[0].Status != domain.StatusInProgress {
		t.Fatalf("changed = %+v", changed)
	}
	if _, err := c.Cancel("anna.intern", r.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("cancel in progress: %v", err)
	}
	if _, err := c.DraftTicket("anna.intern", domain.TicketDetails{Service: "vpn"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("empty summary accepted")
	}
	if _, err := c.DraftTicket("ghost", domain.TicketDetails{Summary: "x"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("ghost ticket")
	}
}

func TestInventory(t *testing.T) {
	c, clk := newCore(t)
	items, holdings := func() ([]domain.ItemView, []domain.Holding) {
		i, h, err := c.Equipment("anna.intern", "monitor")
		if err != nil {
			t.Fatal(err)
		}
		return i, h
	}()
	if len(items) != 3 || len(holdings) != 1 || holdings[0].Has != 1 || holdings[0].Norm != 2 {
		t.Fatalf("items=%v holdings=%+v", items, holdings)
	}
	if _, _, err := c.Equipment("ghost", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("ghost equipment")
	}

	// нет на складе: альтернативы, закупка в пределах нормы возможна
	d := must(c.DraftEquipment("anna.intern", domain.EquipmentDetails{Action: "issue", Item: "dell-27-4k", Reason: "второй монитор"}))
	if d.Outcome != "out_of_stock" || len(d.Alternatives) != 2 || !strings.Contains(d.Hint, "purchase") {
		t.Fatalf("d = %+v", d)
	}
	// выдача из наличия: резерв, через время - готово к выдаче
	d = must(c.DraftEquipment("anna.intern", domain.EquipmentDetails{Action: "issue", Item: "lg-27-qhd", Reason: "второй монитор"}))
	r := must(c.Confirm("anna.intern", d.Request.ID))
	if r.Status != domain.StatusApproved {
		t.Fatalf("status = %s", r.Status)
	}
	changed := tick(c, clk)
	if len(changed) != 1 || changed[0].Status != domain.StatusReady {
		t.Fatalf("changed = %+v", changed)
	}
	// третий монитор - сверх нормы: к наставнику, закупка запрещена
	d = must(c.DraftEquipment("anna.intern", domain.EquipmentDetails{Action: "issue", Item: "lg-24-fhd", Reason: "третий"}))
	if d.Request.Policy.Verdict != domain.VerdictNeedsMentor {
		t.Fatalf("policy = %+v", d.Request.Policy)
	}
	r = must(c.Confirm("anna.intern", d.Request.ID))
	r = must(c.Decide("igor.mentor", r.ID, true, ""))
	if r.Status != domain.StatusApproved {
		t.Fatalf("after mentor = %s", r.Status)
	}
	if _, err := c.DraftEquipment("anna.intern", domain.EquipmentDetails{Action: "purchase", Item: "dell-27-4k", Reason: "x"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("purchase over norm: %v", err)
	}
}

func TestInventory_WaitlistPurchaseReturn(t *testing.T) {
	c, clk := newCore(t)
	d := must(c.DraftEquipment("pavel.intern", domain.EquipmentDetails{Action: "waitlist", Item: "logi-mx-master", Reason: "удобная мышь"}))
	if r := must(c.Confirm("pavel.intern", d.Request.ID)); r.Status != domain.StatusWaitlisted {
		t.Fatalf("waitlist = %s", r.Status)
	}
	d = must(c.DraftEquipment("pavel.intern", domain.EquipmentDetails{Action: "purchase", Item: "dell-27-4k", Reason: "верстка"}))
	if d.Request.Approver != "olga.lead" {
		t.Fatalf("purchase approver = %s", d.Request.Approver)
	}
	if r := must(c.Confirm("pavel.intern", d.Request.ID)); r.Status != domain.StatusInReview {
		t.Fatalf("purchase = %s", r.Status)
	}
	if changed := tick(c, clk); len(changed) != 1 || changed[0].Status != domain.StatusApproved {
		t.Fatalf("purchase decided = %+v", changed)
	}
	stockBefore := func() int {
		items, _, _ := c.Equipment("pavel.intern", "laptop")
		for _, it := range items {
			if it.ID == "mbp-14" {
				return it.Stock
			}
		}
		return -1
	}
	laptops := func() int {
		_, hs, _ := c.Equipment("pavel.intern", "")
		for _, h := range hs {
			if h.Category == "laptop" {
				return h.Has
			}
		}
		return 0
	}
	stock, had := stockBefore(), laptops()
	d = must(c.DraftEquipment("pavel.intern", domain.EquipmentDetails{Action: "return", Item: "mbp-14", Reason: "конец стажировки"}))
	twice := must(c.DraftEquipment("pavel.intern", domain.EquipmentDetails{Action: "return", Item: "mbp-14", Reason: "еще раз"}))
	if r := must(c.Confirm("pavel.intern", d.Request.ID)); r.Status != domain.StatusApproved || r.Resolution == "" {
		t.Fatalf("return = %+v", r)
	}
	if laptops() != had-1 || stockBefore() != stock+1 {
		t.Fatalf("after return: laptops %d (was %d), stock %d (was %d)", laptops(), had, stockBefore(), stock)
	}
	if had == 1 {
		if _, err := c.Confirm("pavel.intern", twice.Request.ID); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("second return of the same laptop: %v", err)
		}
		if _, err := c.DraftEquipment("pavel.intern", domain.EquipmentDetails{Action: "return", Item: "mbp-14", Reason: "x"}); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("return with nothing issued: %v", err)
		}
	}
	for _, e := range []domain.EquipmentDetails{
		{Action: "issue", Item: "nope", Reason: "x"},
		{Action: "issue", Item: "mbp-14", Reason: " "},
		{Action: "steal", Item: "mbp-14", Reason: "x"},
		{Action: "replace", Item: "jabra-75", Reason: "x"},
	} {
		if _, err := c.DraftEquipment("pavel.intern", e); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%+v: %v", e, err)
		}
	}
	if _, err := c.DraftEquipment("ghost", domain.EquipmentDetails{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("ghost")
	}
}

func TestInventory_StockGoneBeforeConfirm(t *testing.T) {
	c, _ := newCore(t)
	var drafts []string
	for range 3 {
		d := must(c.DraftEquipment("anna.intern", domain.EquipmentDetails{Action: "replace", Item: "tp-x1", Reason: "сломался"}))
		drafts = append(drafts, d.Request.ID)
	}
	must(c.Confirm("anna.intern", drafts[0]))
	must(c.Confirm("anna.intern", drafts[1]))
	if _, err := c.Confirm("anna.intern", drafts[2]); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("confirm without stock: %v", err)
	}
}

func TestOnboarding(t *testing.T) {
	c, _ := newCore(t)
	ob := must(c.Onboarding("anna.intern"))
	if ob.Total != 11 || ob.Done != 1 || ob.Next.ID != "meet-team" {
		t.Fatalf("ob = done %d next %+v", ob.Done, ob.Next)
	}
	must(c.CompleteStep("anna.intern", "anna.intern", "meet-team"))
	if _, err := c.CompleteStep("anna.intern", "anna.intern", "setup-env"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("intern completed mentor step: %v", err)
	}
	must(c.CompleteStep("igor.mentor", "anna.intern", "setup-env"))
	if _, err := c.CompleteStep("igor.mentor", "anna.intern", "plan-with-lead"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("mentor completed lead step: %v", err)
	}
	must(c.CompleteStep("olga.lead", "anna.intern", "plan-with-lead"))
	if _, err := c.CompleteStep("maria.mentor", "anna.intern", "setup-env"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("foreign mentor")
	}
	if _, err := c.CompleteStep("anna.intern", "anna.intern", "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("unknown step")
	}
	for _, ids := range [][2]string{{"ghost", "anna.intern"}, {"anna.intern", "ghost"}} {
		if _, err := c.CompleteStep(ids[0], ids[1], "meet-team"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%v", ids)
		}
	}
	ob = must(c.Onboarding("anna.intern"))
	if ob.Done != 4 || ob.Next.ID != "gitlab-access" {
		t.Fatalf("ob = done %d next %+v", ob.Done, ob.Next)
	}
	if _, err := c.Onboarding("ghost"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("ghost onboarding")
	}
}

func TestKB(t *testing.T) {
	c, _ := newCore(t)
	hits := c.SearchKB(context.Background(), "не подключается VPN, ошибка timeout", 5)
	if len(hits) == 0 || hits[0].ID != "vpn-not-connecting" || len(hits) > 3 {
		t.Fatalf("hits = %+v", hits)
	}
	if hits := c.SearchKB(context.Background(), "как оформить отгул", 1); len(hits) != 1 || hits[0].ID != "day-off" {
		t.Fatalf("day off = %+v", hits)
	}
	if hits := c.SearchKB(context.Background(), "впн тормозит", 1); len(hits) != 1 || hits[0].ID != "vpn-slow" {
		t.Fatalf("jargon = %+v", hits)
	}
	if hits := c.SearchKB(context.Background(), "?? ab", 3); hits != nil {
		t.Fatalf("empty query = %v", hits)
	}
	if hits := c.SearchKB(context.Background(), "квантовая криптография", 3); len(hits) != 0 {
		t.Fatalf("nonsense = %v", hits)
	}
}

func TestNew_ValidatesSeed(t *testing.T) {
	good, err := seed.Load("../../../data")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(s *domain.Seed){
		"mentor": func(s *domain.Seed) { s.Users = append(s.Users, domain.User{ID: "x", Mentor: "nobody"}) },
		"grant":  func(s *domain.Seed) { s.Grants = append(s.Grants, domain.Grant{User: "nobody"}) },
		"project": func(s *domain.Seed) {
			s.Grants = append(s.Grants, domain.Grant{User: "anna.intern", Service: "gitlab", Project: "x"})
		},
		"owner": func(s *domain.Seed) {
			s.Services = append(s.Services, domain.Service{ID: "x", Projects: []domain.Project{{ID: "p", Owner: "nobody"}}})
		},
		"issued": func(s *domain.Seed) { s.Issued = append(s.Issued, domain.Issued{User: "anna.intern", Item: "nope"}) },
	}
	for name, mutate := range cases {
		s := good
		s.Users, s.Grants, s.Services, s.Issued = append([]domain.User{}, good.Users...), append([]domain.Grant{}, good.Grants...),
			append([]domain.Service{}, good.Services...), append([]domain.Issued{}, good.Issued...)
		mutate(&s)
		if _, newErr := domain.New(s, domain.Options{}); newErr == nil {
			t.Errorf("%s: invalid seed accepted", name)
		}
	}
	c, err := domain.New(good, domain.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if users := c.Users(); len(users) != 6 || users[0].ID != "anna.intern" {
		t.Fatalf("users = %v", users)
	}
	if _, err := c.User("ghost"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("ghost user")
	}
}

func TestMentees(t *testing.T) {
	c, _ := newCore(t)
	d := must(c.DraftAccess("anna.intern", domain.AccessDetails{Service: "k8s-prod", Project: "prod-cluster", Level: "read", Justification: "поды"}))
	must(c.Confirm("anna.intern", d.Request.ID))

	igor := must(c.Mentees("igor.mentor"))
	if len(igor) != 1 || igor[0].User.ID != "anna.intern" || igor[0].PendingReview != 1 || igor[0].Total != 11 {
		t.Fatalf("igor mentees = %+v", igor)
	}
	if olga := must(c.Mentees("olga.lead")); len(olga) != 2 {
		t.Fatalf("lead mentees = %+v", olga)
	}
	if anna := must(c.Mentees("anna.intern")); len(anna) != 0 {
		t.Fatalf("intern has mentees: %+v", anna)
	}
	if _, err := c.Mentees("ghost"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("ghost mentees")
	}

	if ob := must(c.OnboardingFor("igor.mentor", "anna.intern")); ob.User != "anna.intern" {
		t.Fatalf("ob = %+v", ob)
	}
	must(c.OnboardingFor("anna.intern", "anna.intern"))
	for _, ids := range [][2]string{{"igor.mentor", "pavel.intern"}, {"pavel.intern", "anna.intern"}, {"igor.mentor", "ghost"}, {"ghost", "anna.intern"}} {
		if _, err := c.OnboardingFor(ids[0], ids[1]); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%v: %v", ids, err)
		}
	}
}
