package domain_test

import (
	"encoding/json"
	"maps"
	"testing"

	"github.com/avequa/ai-it-buddy/core/internal/domain"
)

// accumulate - склеивает пачки изменений в снимок, как это делает база (upsert по ключу)
type accumulate struct {
	requests  map[string]domain.Request
	grants    map[[3]string]domain.Grant
	stock     map[string]int
	issued    []domain.Issued
	steps     map[[2]string]domain.StepDone
	incidents map[string]domain.Incident
	notes     map[int64]domain.Notification
	security  []domain.SecurityEvent
	topics    map[[2]string]domain.TopicStat
	flags     map[string]domain.UserFlags
}

func newAccumulate() *accumulate {
	return &accumulate{
		requests: map[string]domain.Request{}, grants: map[[3]string]domain.Grant{}, stock: map[string]int{},
		steps: map[[2]string]domain.StepDone{}, incidents: map[string]domain.Incident{}, notes: map[int64]domain.Notification{},
		topics: map[[2]string]domain.TopicStat{}, flags: map[string]domain.UserFlags{},
	}
}

func (a *accumulate) apply(ch domain.State) {
	for _, r := range ch.Requests {
		a.requests[r.ID] = r
	}
	for _, g := range ch.Grants {
		a.grants[[3]string{g.User, g.Service, g.Project}] = g
	}
	maps.Copy(a.stock, ch.Stock)
	a.issued = append(a.issued, ch.Issued...)
	for _, s := range ch.StepsDone {
		a.steps[[2]string{s.User, s.Step}] = s
	}
	for _, i := range ch.Incidents {
		a.incidents[i.ID] = i
	}
	for _, n := range ch.Notes {
		a.notes[n.ID] = n
	}
	a.security = append(a.security, ch.Security...)
	for _, t := range ch.Topics {
		a.topics[[2]string{t.User, t.Topic}] = t
	}
	for _, f := range ch.Flags {
		a.flags[f.User] = f
	}
}

func (a *accumulate) state() domain.State {
	st := domain.State{Stock: a.stock, Issued: a.issued, Security: a.security}
	for _, v := range a.requests {
		st.Requests = append(st.Requests, v)
	}
	for _, v := range a.grants {
		st.Grants = append(st.Grants, v)
	}
	for _, v := range a.steps {
		st.StepsDone = append(st.StepsDone, v)
	}
	for _, v := range a.incidents {
		st.Incidents = append(st.Incidents, v)
	}
	for _, v := range a.notes {
		st.Notes = append(st.Notes, v)
	}
	for _, v := range a.topics {
		st.Topics = append(st.Topics, v)
	}
	for _, v := range a.flags {
		st.Flags = append(st.Flags, v)
	}
	return st
}

func sameJSON(t *testing.T, what string, x, y any) {
	t.Helper()
	a, _ := json.Marshal(x)
	b, _ := json.Marshal(y)
	if string(a) != string(b) {
		t.Fatalf("%s differ:\n%s\n%s", what, a, b)
	}
}

func TestPersist_TakeChangesRestore(t *testing.T) {
	a, clk := newCore(t)
	db := newAccumulate()
	if a.PendingChanges() != 0 {
		t.Fatalf("pending on start = %d", a.PendingChanges())
	}
	a.MarkAllDirty()
	ch, commit := a.TakeChanges()
	if len(ch.Grants) == 0 || len(ch.Stock) == 0 || len(ch.Issued) == 0 {
		t.Fatalf("initial snapshot = %+v", ch)
	}
	db.apply(ch)
	commit(true)

	d := must(a.DraftAccess("anna.intern", domain.AccessDetails{Service: "grafana", Project: "payments-dashboards", Level: "write", Justification: "алерт"}))
	must(a.Confirm("anna.intern", d.Request.ID))
	e := must(a.DraftEquipment("anna.intern", domain.EquipmentDetails{Action: "issue", Item: "lg-27-qhd", Reason: "второй"}))
	must(a.Confirm("anna.intern", e.Request.ID))

	// неудачная запись: изменения не теряются и уходят в следующую пачку вместе с новыми
	failed, commit := a.TakeChanges()
	commit(false)
	if a.PendingChanges() == 0 {
		t.Fatal("changes lost after failed save")
	}
	tick(a, clk)
	must(a.CompleteStep("anna.intern", "anna.intern", "meet-team"))
	inc := must(a.OpenIncident("sergey.sre", "vpn", "Шлюз недоступен", ""))
	must(a.ResolveIncident("sergey.sre", inc.ID))
	a.RecordSecurity("pavel.intern", domain.SecForeign, "ACC-1")
	a.TrackTopic("anna.intern", "VPN")
	a.TrackKBMiss("pavel.intern")
	a.OfferStarterPack("pavel.intern")
	notes := a.Notifications("anna.intern", 0)
	a.MarkRead("anna.intern", notes[0].ID)

	ch, commit = a.TakeChanges()
	if len(ch.Requests) < len(failed.Requests) || len(ch.Issued) != 1 || len(ch.Security) != 1 {
		t.Fatalf("retry batch: %d requests (failed had %d), %d issued, %d security", len(ch.Requests), len(failed.Requests), len(ch.Issued), len(ch.Security))
	}
	db.apply(ch)
	commit(true)
	if n := a.PendingChanges(); n != 0 {
		t.Fatalf("pending = %d", n)
	}
	if ch, _ := a.TakeChanges(); !ch.Empty() || ch.Size() != 0 {
		t.Fatalf("empty batch expected: %+v", ch)
	}

	b, _ := newCore(t)
	b.Restore(db.state())
	for _, u := range []string{"anna.intern", "pavel.intern", "igor.mentor"} {
		sameJSON(t, u+" requests", a.MyRequests(u, ""), b.MyRequests(u, ""))
		sameJSON(t, u+" notifications", a.Notifications(u, 0), b.Notifications(u, 0))
		ga, _, _ := a.MyAccess(u)
		gb, _, _ := b.MyAccess(u)
		sameJSON(t, u+" grants", ga, gb)
	}
	oa, _ := a.Onboarding("anna.intern")
	ob, _ := b.Onboarding("anna.intern")
	sameJSON(t, "onboarding", oa, ob)
	ia, ha, _ := a.Equipment("anna.intern", "")
	ib, hb, _ := b.Equipment("anna.intern", "")
	sameJSON(t, "equipment", []any{ia, ha}, []any{ib, hb})
	sameJSON(t, "incidents", a.Incidents(), b.Incidents())
	sameJSON(t, "security", a.SecurityEvents("igor.mentor"), b.SecurityEvents("igor.mentor"))
	da, _ := a.Digest("igor.mentor", "anna.intern")
	dbg, _ := b.Digest("igor.mentor", "anna.intern")
	sameJSON(t, "digest", da, dbg)
	if b.OfferStarterPack("pavel.intern") {
		t.Fatal("starter pack offered again")
	}

	// номера продолжаются после восстановления
	n := must(b.DraftAccess("pavel.intern", domain.AccessDetails{Service: "jira", Project: "PAY", Level: "write", Justification: "x"}))
	if n.Request != nil && n.Request.ID == d.Request.ID {
		t.Fatalf("id reused: %s", n.Request.ID)
	}
	if b.PendingChanges() == 0 && n.Request != nil {
		t.Fatal("new draft after restore not marked")
	}

	// записи о неизвестных сотрудниках и позициях пропускаются
	c, _ := newCore(t)
	c.Restore(domain.State{
		Grants: []domain.Grant{{User: "ghost", Service: "vpn", Project: "corp", Level: "read"}},
		Stock:  map[string]int{"gone": 3},
		Issued: []domain.Issued{{User: "anna.intern", Item: "gone"}},
	})
	if g, _, _ := c.MyAccess("anna.intern"); len(g) != 0 {
		t.Fatalf("grants = %+v", g)
	}
	if _, h, _ := c.Equipment("anna.intern", ""); len(h) != 0 && h[0].Has != 0 {
		t.Fatalf("holdings = %+v", h)
	}
}
