package support

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

type fixture struct {
	s                        *Service
	pool                     *pgxpool.Pool
	customer, manager, other Actor
	now                      time.Time
}

func supportDB(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL required for PostgreSQL support tests")
	}
	ctx := t.Context()
	root, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := "support_test_" + strings.ReplaceAll(newID(), "-", "")
	if _, e = root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = root.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		root.Close()
	})
	ddl, e := os.ReadFile("schema.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, string(ddl)); e != nil {
		t.Fatal(e)
	}
	f := &fixture{pool: pool, now: at(t, "2026-11-11T10:00:00Z"), customer: Actor{ID: newID(), Session: true}, manager: Actor{ID: newID(), Session: true}, other: Actor{ID: newID(), Session: true}}
	for _, a := range []Actor{f.customer, f.manager, f.other} {
		if _, e = pool.Exec(ctx, `INSERT INTO accounts(id,email,plan,status,email_verified_at,mfa_enrolled_at) VALUES($1,$2,'pro','active',now(),now())`, a.ID, a.ID+"@example.com"); e != nil {
			t.Fatal(e)
		}
	}
	f.s = &Service{Pool: pool, Now: func() time.Time { return f.now }, Operator: func(id, email string) bool { return id == f.manager.ID }}
	return f
}
func (f *fixture) call(t *testing.T, a Actor, staff bool, endpoint, ticket, key, query string, in any, want int) *httptest.ResponseRecorder {
	t.Helper()
	method := "GET"
	var b []byte
	if in != nil {
		method = "POST"
		b = raw(in)
	}
	r := httptest.NewRequest(method, "/support"+query, bytes.NewReader(b))
	r.SetPathValue("ticket", ticket)
	if key == "" {
		key = newID()
	}
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	f.s.Serve(w, r, a, endpoint, staff)
	if w.Code != want {
		t.Fatalf("%s: status %d want %d: %s", endpoint, w.Code, want, w.Body.String())
	}
	return w
}
func unpack[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	return v
}
func (f *fixture) create(t *testing.T, body string) string {
	t.Helper()
	w := f.call(t, f.customer, false, "create", "", "", "", api.CreateSupportTicketRequest{Subject: "Deployment issue", Category: "deployment", Body: body}, 202)
	return unpack[api.SupportSubmission](t, w).TicketID
}
func (f *fixture) change(t *testing.T, a Actor, staff bool, id, op string, revision int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	return f.call(t, a, staff, op, id, "", "", Change{ExpectedRevision: revision, Body: body}, 202)
}
func TestSupportScopeAndCurrentRevocation(t *testing.T) {
	f := supportDB(t)
	id := f.create(t, "customer details")
	f.call(t, f.other, false, "get", id, "", "", nil, 404)
	f.call(t, f.customer, true, "get", id, "", "", nil, 403)
	key := newID()
	in := Change{ExpectedRevision: 1, Body: "another detail"}
	f.call(t, f.customer, false, "reply", id, key, "", in, 202)
	if _, e := f.pool.Exec(t.Context(), `UPDATE accounts SET status='suspended' WHERE id=$1`, f.customer.ID); e != nil {
		t.Fatal(e)
	}
	f.call(t, f.customer, false, "reply", id, key, "", in, 403)
	f.manager.Session = false
	f.call(t, f.manager, true, "get", id, "", "", nil, 403)
}
func TestSupportPoliciesPauseReopenAndPermanentReceipts(t *testing.T) {
	f := supportDB(t)
	p := defaultPolicy()
	p.ID = newID()
	p.Revision = 0
	p.Plan = "pro"
	p.Priority = "high"
	p.ResolutionSeconds = 3600
	p.ReopenWindowSeconds = 3600
	p.CustomerReopensMax = 1
	f.call(t, f.manager, true, "policy_save", "", "", "", p, 202)
	w := f.call(t, f.customer, false, "create", "", "", "", api.CreateSupportTicketRequest{Subject: "Need assistance", Category: "other", Priority: "high", Body: "help"}, 202)
	ticket := unpack[api.SupportSubmission](t, w).TicketID
	got := unpack[StaffTicket](t, f.call(t, f.manager, true, "get", ticket, "", "", nil, 200))
	if got.Episode.Policy.ID != p.ID || got.Episode.Policy.Revision != 1 {
		t.Fatalf("policy snapshot: %+v", got)
	}
	p.Revision = 1
	p.ResolutionSeconds = 7200
	f.call(t, f.manager, true, "policy_save", "", "", "", p, 202)
	f.change(t, f.manager, true, ticket, "reply", 1, "Please check this setting")
	f.now = f.now.Add(30 * time.Minute)
	f.change(t, f.manager, true, ticket, "pause", 2, "")
	f.now = f.now.Add(48 * time.Hour)
	f.change(t, f.customer, false, ticket, "reply", 3, "Done")
	f.change(t, f.manager, true, ticket, "close", 4, "")
	got = unpack[StaffTicket](t, f.call(t, f.manager, true, "get", ticket, "", "", nil, 200))
	oldEpisode := got.Episode.ID
	if got.Episode.ResolutionBreached || got.Episode.Policy.ResolutionSeconds != 3600 || got.Episode.PausedUS != int64(48*time.Hour/time.Microsecond) {
		t.Fatalf("clock snapshot: %+v", got.Episode)
	}
	f.call(t, f.customer, false, "satisfaction", ticket, "", "", api.SupportSatisfactionRequest{EpisodeID: oldEpisode, Rating: 5}, 202)
	key := newID()
	in := Change{ExpectedRevision: 5, Body: "Still failing"}
	first := unpack[api.SupportSubmission](t, f.call(t, f.customer, false, "reopen", ticket, key, "", in, 202))
	got = unpack[StaffTicket](t, f.call(t, f.manager, true, "get", ticket, "", "", nil, 200))
	if got.Episode.ID == oldEpisode || got.CustomerReopens != 1 || got.Episode.Policy.ResolutionSeconds != 3600 {
		t.Fatalf("reopened episode: %+v", got)
	}
	f.now = f.now.Add(31 * 24 * time.Hour)
	replay := unpack[api.SupportSubmission](t, f.call(t, f.customer, false, "reopen", ticket, key, "", in, 202))
	if first != replay {
		t.Fatalf("late receipt changed: %+v %+v", first, replay)
	}
	f.change(t, f.manager, true, ticket, "close", 6, "")
	f.call(t, f.customer, false, "reopen", ticket, "", "", Change{ExpectedRevision: 7, Body: "again"}, 409)
	survey := unpack[api.SupportSatisfaction](t, f.call(t, f.customer, false, "satisfaction", ticket, "", "", nil, 200))
	if survey.EpisodeID == oldEpisode || !survey.Eligible {
		t.Fatal("survey was not bound to new genuine resolution")
	}
	var n int
	if e := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM support_episodes WHERE ticket_id=$1`, ticket).Scan(&n); e != nil || n != 2 {
		t.Fatalf("episodes: %d %v", n, e)
	}
}
func TestSupportRedactionRemovesCopiesSearchAndDrafts(t *testing.T) {
	f := supportDB(t)
	f.s.Webhook, _ = NewWebhook("https://helpdesk.example.com/events", strings.Repeat("x", 32))
	secret := "credential_very_sensitive_98765"
	ticket := f.create(t, secret)
	f.change(t, f.manager, true, ticket, "note", 1, secret)
	f.call(t, f.manager, true, "staff_save", "", "", "", Staff{AccountID: f.manager.ID, Role: "manager", Profile: Profile{Available: true, Capacity: 50}}, 202)
	eng := Engineering{ExpectedRevision: 2, OwnerID: f.manager.ID, Severity: "high", Status: "investigating", Reproduction: "private reproduction"}
	f.call(t, f.manager, true, "engineering_save", ticket, "", "", eng, 202)
	f.call(t, f.manager, true, "engineering_preview", ticket, "", "", PreviewRequest{ExpectedRevision: 3, EngineeringRevision: 1, Body: secret}, 202)
	msgs := unpack[api.SupportMessagePage](t, f.call(t, f.manager, true, "messages", ticket, "", "", nil, 200))
	preview := unpack[Preview](t, f.call(t, f.manager, true, "redaction_preview", ticket, "", "", PreviewRequest{ExpectedRevision: 3, MessageID: msgs.Items[0].ID, Reason: "secret"}, 202))
	if len(preview.MessageIDs) != 2 || strings.Contains(string(raw(preview)), secret) {
		t.Fatalf("unsafe preview: %+v", preview)
	}
	key := newID()
	apply := Approval{PreviewID: preview.ID, ExpectedRevision: 3}
	f.call(t, f.manager, true, "redaction_apply", ticket, key, "", apply, 202)
	messages := unpack[api.SupportMessagePage](t, f.call(t, f.customer, false, "messages", ticket, "", "", nil, 200))
	if len(messages.Items) != 1 || messages.Items[0].Body != "[redacted]" || !messages.Items[0].Redacted {
		t.Fatalf("public redaction: %+v", messages)
	}
	result := unpack[api.SupportTicketPage](t, f.call(t, f.customer, false, "list", "", "", "?search="+secret, nil, 200))
	if len(result.Items) != 0 {
		t.Fatal("redacted text remained searchable")
	}
	for _, table := range []string{"support_messages", "support_receipts", "support_events", "support_outbox", "support_previews"} {
		var n int
		e := f.pool.QueryRow(t.Context(), fmt.Sprintf(`SELECT count(*) FROM %s t WHERE row_to_json(t)::text LIKE $1`, pgx.Identifier{table}.Sanitize()), "%"+secret+"%").Scan(&n)
		if e != nil || n != 0 {
			t.Fatalf("secret in %s: %d %v", table, n, e)
		}
	}
	f.now = f.now.Add(24 * time.Hour)
	f.call(t, f.manager, true, "redaction_apply", ticket, key, "", apply, 202)
}
func TestSupportEngineeringReviewsAreBoundAndPrivate(t *testing.T) {
	f := supportDB(t)
	ticket := f.create(t, "help")
	f.call(t, f.manager, true, "staff_save", "", "", "", Staff{AccountID: f.manager.ID, Role: "manager", Profile: Profile{Available: true, Capacity: 50}}, 202)
	in := Engineering{ExpectedRevision: 1, OwnerID: f.manager.ID, Severity: "critical", Status: "resolved", Reproduction: "internal-only-stacktrace", IssueURL: "https://tracker.example.com/issues/42"}
	f.call(t, f.manager, true, "engineering_save", ticket, "", "", in, 202)
	pub := f.call(t, f.customer, false, "get", ticket, "", "", nil, 200)
	if strings.Contains(pub.Body.String(), "internal-only") || strings.Contains(pub.Body.String(), "owner_id") || strings.Contains(pub.Body.String(), `"status":"resolved"`) {
		t.Fatal("engineering data leaked or closed customer case")
	}
	p := unpack[Preview](t, f.call(t, f.manager, true, "engineering_preview", ticket, "", "", PreviewRequest{ExpectedRevision: 2, EngineeringRevision: 1, Body: "A fix is ready for you to try."}, 202))
	f.change(t, f.manager, true, ticket, "note", 2, "review context")
	f.call(t, f.manager, true, "engineering_publish", ticket, "", "", Approval{PreviewID: p.ID, ExpectedRevision: 2}, 409)
	p = unpack[Preview](t, f.call(t, f.manager, true, "engineering_preview", ticket, "", "", PreviewRequest{ExpectedRevision: 3, EngineeringRevision: 1, Body: "A fix is ready for you to try."}, 202))
	f.call(t, f.manager, true, "engineering_publish", ticket, "", "", Approval{PreviewID: p.ID, ExpectedRevision: 3}, 202)
	page := unpack[api.SupportMessagePage](t, f.call(t, f.customer, false, "messages", ticket, "", "", nil, 200))
	if len(page.Items) != 2 || page.Items[1].Body != p.Body {
		t.Fatalf("published body: %+v", page)
	}
}
func TestSupportRoutingCapacityFallbackAndCycle(t *testing.T) {
	f := supportDB(t)
	teamA, teamB := newID(), newID()
	for _, a := range []Actor{f.other, f.manager} {
		f.call(t, f.manager, true, "staff_save", "", "", "", Staff{AccountID: a.ID, Role: "agent", Profile: Profile{Available: true, Capacity: 1, Skills: []string{"deploy"}}}, 202)
	}
	// The bootstrap manager now has an explicit agent role; grant management through a second bootstrap account.
	admin := Actor{ID: newID(), Session: true}
	if _, e := f.pool.Exec(t.Context(), `INSERT INTO accounts VALUES($1,$2,'pro','active',now(),now())`, admin.ID, admin.ID+"@example.com"); e != nil {
		t.Fatal(e)
	}
	f.s.Operator = func(id, email string) bool { return id == admin.ID }
	f.call(t, admin, true, "team_save", "", "", "", Team{ID: teamB, Name: "Fallback", Enabled: true, Members: []string{f.other.ID}}, 202)
	f.call(t, admin, true, "team_save", "", "", "", Team{ID: teamA, Name: "Primary", Enabled: true, Members: []string{f.manager.ID}, FallbackID: teamB}, 202)
	f.call(t, admin, true, "team_save", "", "", "", Team{ID: teamB, Revision: 1, Name: "Cycle", Enabled: true, Members: []string{f.other.ID}, FallbackID: teamA}, 400)
	p := defaultPolicy()
	p.ID = newID()
	p.Revision = 0
	p.TeamID = teamA
	f.call(t, admin, true, "policy_save", "", "", "", p, 202)
	var wg sync.WaitGroup
	failures := make(chan string, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodPost, "/support", bytes.NewReader(raw(api.CreateSupportTicketRequest{Subject: "Concurrent issue", Category: "deployment", Body: "Help"})))
			r.Header.Set("Idempotency-Key", newID())
			w := httptest.NewRecorder()
			f.s.Serve(w, r, f.customer, "create", false)
			if w.Code != 202 {
				failures <- w.Body.String()
			}
		}()
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	var assigned, total int
	if e := f.pool.QueryRow(t.Context(), `SELECT count(assignee_id),count(*) FROM support_tickets`).Scan(&assigned, &total); e != nil || assigned != 2 || total != 4 {
		t.Fatalf("capacity assignment: %d/%d %v", assigned, total, e)
	}
}

func TestSupportOrganizationKeyAndMembershipBoundaries(t *testing.T) {
	f := supportDB(t)
	orgID := newID()
	if _, e := f.pool.Exec(t.Context(), `INSERT INTO orgs(id,slug,plan,deleted_pending,personal_org) VALUES($1,'acme','pro',false,false)`, orgID); e != nil {
		t.Fatal(e)
	}
	if _, e := f.pool.Exec(t.Context(), `INSERT INTO org_memberships(org_id,account_id,role) VALUES($1,$2,'owner')`, orgID, f.customer.ID); e != nil {
		t.Fatal(e)
	}
	key := newID()
	in := api.CreateSupportTicketRequest{Subject: "Org case", Category: "billing", Body: "Please check billing"}
	call := func(a Actor, want int) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/orgs/acme/support/tickets", bytes.NewReader(raw(in)))
		r.SetPathValue("org", "acme")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		f.s.Serve(w, r, a, "create", false)
		if w.Code != want {
			t.Fatalf("org create %d want %d: %s", w.Code, want, w.Body)
		}
		return w
	}
	actor := f.customer
	actor.Session = false
	actor.KeyOrgID = orgID
	ticket := unpack[api.SupportSubmission](t, call(actor, 202)).TicketID
	f.call(t, f.customer, false, "get", ticket, "", "", nil, 404)
	actor.KeyOrgID = newID()
	call(actor, 403)
	actor.KeyOrgID = orgID
	if _, e := f.pool.Exec(t.Context(), `UPDATE org_memberships SET role='developer' WHERE org_id=$1`, orgID); e != nil {
		t.Fatal(e)
	}
	call(actor, 403)
	if _, e := f.pool.Exec(t.Context(), `UPDATE org_memberships SET role='owner',removed_at=now() WHERE org_id=$1`, orgID); e != nil {
		t.Fatal(e)
	}
	call(actor, 403)
}
func TestSupportStaleExpiredReviewsAndZeroReopeningWindow(t *testing.T) {
	f := supportDB(t)
	p := defaultPolicy()
	p.ID = newID()
	p.Revision = 0
	p.ReopenWindowSeconds = 0
	f.call(t, f.manager, true, "policy_save", "", "", "", p, 202)
	ticket := f.create(t, "content")
	page := unpack[api.SupportMessagePage](t, f.call(t, f.manager, true, "messages", ticket, "", "", nil, 200))
	input := PreviewRequest{ExpectedRevision: 1, MessageID: page.Items[0].ID, Reason: "personal_data"}
	preview := unpack[Preview](t, f.call(t, f.manager, true, "redaction_preview", ticket, "", "", input, 202))
	f.now = f.now.Add(16 * time.Minute)
	f.call(t, f.manager, true, "redaction_apply", ticket, "", "", Approval{PreviewID: preview.ID, ExpectedRevision: 1}, 409)
	f.change(t, f.manager, true, ticket, "close", 1, "")
	got := unpack[api.SupportTicket](t, f.call(t, f.customer, false, "get", ticket, "", "", nil, 200))
	if got.CanReopen {
		t.Fatal("disabled customer reopening was allowed")
	}
	f.call(t, f.customer, false, "reopen", ticket, "", "", Change{ExpectedRevision: 2, Body: "More"}, 409)
	f.call(t, f.customer, false, "list", "", "", "?assignee_id="+f.manager.ID, nil, 400)
}
func TestSupportReportsSeparatePendingAndCompletedAndSnapshotGroups(t *testing.T) {
	f := supportDB(t)
	ticket := f.create(t, "help")
	f.change(t, f.manager, true, ticket, "reply", 1, "Try this")
	f.change(t, f.manager, true, ticket, "pause", 2, "")
	f.now = f.now.Add(48 * time.Hour)
	result, e := f.s.transact(t.Context(), f.manager, "", true, false, func(x *session) (any, error) { return x.report(f.now.Add(-72*time.Hour), f.now) })
	if e != nil {
		t.Fatal(e)
	}
	report := result.(Report)
	if len(report.Groups) != 1 {
		t.Fatalf("groups: %+v", report)
	}
	g := report.Groups[0]
	if g.FirstResponse.Met != 1 || g.Resolution.Pending != 1 || g.Resolution.Breached != 0 || g.PausedSeconds != int64(48*time.Hour/time.Second) || g.NextReply.Pending != 0 {
		t.Fatalf("report: %+v", g)
	}
	f.change(t, f.manager, true, ticket, "close", 3, "")
	result, e = f.s.transact(t.Context(), f.manager, "", true, false, func(x *session) (any, error) { return x.report(f.now.Add(-72*time.Hour), f.now) })
	if e != nil {
		t.Fatal(e)
	}
	g = result.(Report).Groups[0]
	if g.Resolution.Met != 1 || g.Resolution.Pending != 0 {
		t.Fatalf("completed report: %+v", g)
	}
}

func TestSupportSkillsAvailabilityHandoffAndCapacityOverride(t *testing.T) {
	f := supportDB(t)
	team := newID()
	for _, a := range []Actor{f.other, f.manager} {
		role := "agent"
		skills := []string{"deploy"}
		if a.ID == f.manager.ID {
			role = "manager"
			skills = []string{"billing"}
		}
		f.call(t, f.manager, true, "staff_save", "", "", "", Staff{AccountID: a.ID, Role: role, Profile: Profile{Available: true, Capacity: 1, Skills: skills}}, 202)
	}
	f.call(t, f.manager, true, "team_save", "", "", "", Team{ID: team, Name: "Support", Enabled: true, Members: []string{f.manager.ID, f.other.ID}}, 202)
	first := f.create(t, "first")
	in := Change{ExpectedRevision: 1, Priority: "high", TeamID: team, RequiredSkills: []string{"deploy"}, Reason: "skill routing", Route: true}
	f.call(t, f.manager, true, "triage", first, "", "", in, 202)
	got := unpack[StaffTicket](t, f.call(t, f.manager, true, "get", first, "", "", nil, 200))
	if got.AssigneeID != f.other.ID {
		t.Fatalf("skills selected wrong person: %+v", got)
	}
	f.call(t, f.other, true, "availability", "", "", "", Availability{ExpectedRevision: 1, Available: false}, 202)
	second := f.create(t, "second")
	manual := Change{ExpectedRevision: 1, Priority: "high", TeamID: team, AssigneeID: f.other.ID, RequiredSkills: []string{"deploy"}, Reason: "manual handoff", OverrideCapacity: true}
	f.call(t, f.manager, true, "triage", second, "", "", manual, 400)
	f.call(t, f.other, true, "availability", "", "", "", Availability{ExpectedRevision: 2, Available: true}, 202)
	manual.OverrideCapacity = false
	f.call(t, f.manager, true, "triage", second, "", "", manual, 409)
	manual.OverrideCapacity = true
	f.call(t, f.manager, true, "triage", second, "", "", manual, 202)
	events := f.call(t, f.manager, true, "events", second, "", "", nil, 200)
	if !strings.Contains(events.Body.String(), `"capacity_override":true`) || !strings.Contains(events.Body.String(), f.other.ID) {
		t.Fatalf("handoff audit missing: %s", events.Body)
	}
	profile := unpack[map[string]json.RawMessage](t, f.call(t, f.manager, true, "staff", "", "", "", nil, 200))
	if !strings.Contains(string(profile["items"]), `"capacity":1`) || !strings.Contains(string(profile["items"]), "deploy") {
		t.Fatal("availability changed protected profile fields")
	}
}

func TestSupportEngineeringAcceptsMaximumEscapedUTF8Body(t *testing.T) {
	f := supportDB(t)
	ticket := f.create(t, "help")
	f.call(t, f.manager, true, "staff_save", "", "", "", Staff{AccountID: f.manager.ID, Role: "manager", Profile: Profile{Available: true, Capacity: 50}}, 202)
	steps := strings.Repeat("\x01", api.SupportBodyMaxBytes)
	f.call(t, f.manager, true, "engineering_save", ticket, "", "", Engineering{ExpectedRevision: 1, OwnerID: f.manager.ID, Severity: "normal", Status: "investigating", Reproduction: steps}, 202)
	got := unpack[Engineering](t, f.call(t, f.manager, true, "engineering", ticket, "", "", nil, 200))
	if got.Reproduction != steps {
		t.Fatal("escaped reproduction changed")
	}
}

func TestSupportPersonalKeyCannotReachAnotherPersonalScope(t *testing.T) {
	f := supportDB(t)
	org := newID()
	if _, e := f.pool.Exec(t.Context(), `INSERT INTO orgs(id,slug,plan,deleted_pending,personal_org,personal_owner_account_id) VALUES($1,'personal','pro',false,true,$2)`, org, f.customer.ID); e != nil {
		t.Fatal(e)
	}
	a := f.customer
	a.Session = false
	a.KeyOrgID = org
	f.call(t, a, false, "create", "", "", "", api.CreateSupportTicketRequest{Subject: "My case", Category: "account", Body: "Help"}, 202)
	a.ID = f.other.ID
	f.call(t, a, false, "list", "", "", "", nil, 403)
}

func TestSupportRejectsPolicyWhoseCalendarCannotFitItsTarget(t *testing.T) {
	f := supportDB(t)
	p := defaultPolicy()
	p.ID = newID()
	p.Revision = 0
	p.FirstResponseSeconds = 60
	p.NextReplySeconds = 60
	p.ResolutionSeconds = int64(api.SupportTargetMax / time.Second)
	p.Calendar = Calendar{Timezone: "UTC", Weekdays: []int{0}, StartMinute: 0, EndMinute: 1}
	f.call(t, f.manager, true, "policy_save", "", "", "", p, 400)
	ticket := f.create(t, "Default policy remains available")
	got := unpack[StaffTicket](t, f.call(t, f.manager, true, "get", ticket, "", "", nil, 200))
	if got.Episode.Policy.ID != "default" {
		t.Fatal("rejected policy affected ticket intake")
	}
}

func TestSupportUUIDSpellingCannotBypassCyclesOrBreakReceipts(t *testing.T) {
	f := supportDB(t)
	ticket := f.create(t, "Help")
	key := newID()
	in := Change{ExpectedRevision: 1, Body: "Details"}
	f.call(t, f.customer, false, "reply", strings.ToUpper(ticket), key, "", in, 202)
	f.call(t, f.customer, false, "reply", ticket, key, "", in, 202)
	f.call(t, f.customer, false, "reply", strings.ToUpper(ticket), key, "", in, 202)
	teamID := newID()
	team := Team{ID: strings.ToUpper(teamID), Name: "Support", Enabled: true, FallbackID: teamID}
	f.call(t, f.manager, true, "team_save", "", "", "", team, 400)
	team.FallbackID = ""
	key = newID()
	f.call(t, f.manager, true, "team_save", "", key, "", team, 202)
	f.call(t, f.manager, true, "team_save", "", key, "", team, 202)
}

func TestSupportAgentCanClaimOwnUUIDInEitherSpelling(t *testing.T) {
	f := supportDB(t)
	f.call(t, f.manager, true, "staff_save", "", "", "", Staff{AccountID: f.other.ID, Role: "agent", Profile: Profile{Available: true, Capacity: 50}}, 202)
	ticket := f.create(t, "Help")
	f.call(t, f.other, true, "triage", ticket, "", "", Change{ExpectedRevision: 1, Priority: "normal", AssigneeID: strings.ToUpper(f.other.ID), Reason: "claim"}, 202)
}
