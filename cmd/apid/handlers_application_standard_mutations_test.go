package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/state"
)

func installStandardMutationFixture(t *testing.T, e testEnv) state.ApplicationStandardEnrollment {
	t.Helper()
	claim, err := e.store.ClaimApplicationStandardEnrollment(t.Context(), "mutation-api-worker")
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := e.store.MaterializeApplicationStandardEnrollment(t.Context(), claim)
	if err != nil {
		t.Fatal(err)
	}
	return enrollment
}

func standardMutationExceptionRequest(t *testing.T, e testEnv, org state.Org, revision int64) api.ApproveApplicationStandardExceptionRequest {
	t.Helper()
	assignments, err := e.store.ListApplicationStandardAssignments(t.Context(), org.ID)
	if err != nil || len(assignments) != 1 {
		t.Fatalf("fixture assignment: %+v %v", assignments, err)
	}
	return api.ApproveApplicationStandardExceptionRequest{ExpectedRevision: revision, StandardID: assignments[0].StandardID, Version: 1, Field: appstandards.EgressCIDRs, Value: json.RawMessage(`["8.8.0.0/16"]`), Reason: "Resolver migration", ExpiresAt: time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)}
}

func TestApplicationStandardMutationAPIAndSDKLifecycle(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.WithApplicationStandardMutationsEnabled(true)
	org, app := standardEnrollmentAPIFixture(t.Context(), t, e)
	before := installStandardMutationFixture(t, e)
	ts := httptest.NewServer(e.h)
	defer ts.Close()
	client := api.NewClient(ts.URL, e.key)
	ctx := t.Context()
	local := api.SetApplicationStandardLocalIntentRequest{ExpectedRevision: 1, Settings: json.RawMessage(`{"egress_cidrs":["8.8.8.8/32"]}`), AdditionalLogDestinations: []string{}}
	got, err := client.SetApplicationStandardLocalIntent(ctx, org.Slug, app.ID, local)
	if err != nil || got.State != "pending" || got.DesiredRevision != 2 || got.PersistedRevision != 1 || got.ObservedRevision != 0 || got.InstalledEffectiveHash != before.EffectiveHash {
		t.Fatalf("saved intent changed installed truth: %+v %v", got, err)
	}
	base := "/v1/orgs/" + org.Slug + "/application-standard-enrollments/" + app.ID
	assertProblem(t, e.do(t, http.MethodPut, base+"/local-intent", local, nil), http.StatusConflict, api.CodeApplicationStandardVersionStale)
	local.ExpectedRevision = 2
	assertProblem(t, e.do(t, http.MethodPut, base+"/local-intent", local, nil), http.StatusConflict, api.CodeApplicationStandardsPending)
	installStandardMutationFixture(t, e)
	invalid := local
	invalid.Settings = json.RawMessage(`{"log_destinations":[]}`)
	assertProblem(t, e.do(t, http.MethodPut, base+"/local-intent", invalid, nil), http.StatusBadRequest, api.CodeValidation)
	x, err := client.ApproveApplicationStandardException(ctx, org.Slug, app.ID, standardMutationExceptionRequest(t, e, org, 2))
	if err != nil || x.Status != "active" || uuid.MustParse(x.ApprovedBy) != uuid.MustParse(e.acct.ID) || x.Reason != "Resolver migration" || string(x.Value) != `["8.8.0.0/16"]` {
		t.Fatalf("approval: %+v %v", x, err)
	}
	installed := installStandardMutationFixture(t, e)
	if installed.DesiredRevision != 3 || installed.ExceptionExpiresAt == nil || !installed.ExceptionExpiresAt.Equal(x.ExpiresAt) || installed.ObservedRevision != 0 {
		t.Fatalf("exception projection: %+v", installed)
	}
	local.ExpectedRevision, local.Settings = 3, json.RawMessage(`{"egress_cidrs":["8.8.4.4/32"]}`)
	if _, err := client.SetApplicationStandardLocalIntent(ctx, org.Slug, app.ID, local); err != nil {
		t.Fatal(err)
	}
	installStandardMutationFixture(t, e)
	assertProblem(t, e.do(t, http.MethodPost, base+"/exceptions/"+x.ID+"/revoke", api.RevokeApplicationStandardExceptionRequest{ExpectedRevision: 3}, nil), http.StatusConflict, api.CodeApplicationStandardVersionStale)
	revoked, err := client.RevokeApplicationStandardException(ctx, org.Slug, app.ID, x.ID, api.RevokeApplicationStandardExceptionRequest{ExpectedRevision: 4})
	if err != nil || revoked.Status != "revoked" || uuid.MustParse(revoked.RevokedBy) != uuid.MustParse(e.acct.ID) || revoked.RevokedAt == nil || revoked.Reason != x.Reason || !revoked.CreatedAt.Equal(x.CreatedAt) || string(revoked.Value) != string(x.Value) {
		t.Fatalf("revocation erased approval: %+v %v", revoked, err)
	}
	blocked := installStandardMutationFixture(t, e)
	if blocked.State != "blocked" || blocked.DesiredRevision != 5 || blocked.PersistedRevision != 4 || blocked.ObservedRevision != 0 {
		t.Fatalf("revoked constraints were bypassed: %+v", blocked)
	}
	local.ExpectedRevision, local.Settings = 5, json.RawMessage(`{}`)
	if _, err := client.SetApplicationStandardLocalIntent(ctx, org.Slug, app.ID, local); err != nil {
		t.Fatal(err)
	}
	final := installStandardMutationFixture(t, e)
	if final.State != "persisted" || final.DesiredRevision != 6 || final.PersistedRevision != 6 || final.ObservedRevision != 0 || final.ExceptionExpiresAt != nil || string(final.Effective.Values[appstandards.EgressCIDRs]) != `["8.8.8.0/24"]` {
		t.Fatalf("clear did not restore inheritance: %+v", final)
	}
	history, err := client.ListApplicationStandardExceptions(ctx, org.Slug, app.ID, "", 0)
	if err != nil || len(history.Exceptions) != 1 || history.Exceptions[0].Status != "revoked" {
		t.Fatalf("retained history: %+v %v", history, err)
	}
	assertStandardMutationResponsePrivate(t, e.do(t, http.MethodGet, base, nil, nil).Body.String())
}

func assertStandardMutationResponsePrivate(t *testing.T, body string) {
	t.Helper()
	for _, secret := range []string{"private-logs.example.com", "sealed-private-credential", "auth_header", "base_settings", "lease_owner"} {
		if strings.Contains(body, secret) {
			t.Fatalf("mutation response exposed %s", secret)
		}
	}
}

func TestApplicationStandardMutationReleaseGate(t *testing.T) {
	for _, value := range []string{"", "0", "true", "yes", "enabled"} {
		if applicationStandardMutationsEnabledFromEnv(func(string) string { return value }) {
			t.Fatalf("gate accepted %q", value)
		}
	}
	if !applicationStandardMutationsEnabledFromEnv(func(string) string { return " 1 " }) {
		t.Fatal("explicit opt-in was refused")
	}
	e := setup(t, api.PlanPro)
	org, app := standardEnrollmentAPIFixture(t.Context(), t, e)
	before, err := e.store.GetApplicationStandardEnrollment(t.Context(), org.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/orgs/" + org.Slug + "/application-standard-enrollments/" + app.ID
	for _, tc := range []struct{ method, path string }{{http.MethodPut, base + "/local-intent"}, {http.MethodPost, base + "/exceptions"}, {http.MethodPost, base + "/exceptions/" + uuid.NewString() + "/revoke"}} {
		assertProblem(t, e.do(t, tc.method, tc.path, json.RawMessage(`{}`), nil), http.StatusServiceUnavailable, api.CodeApplicationStandardsPending)
	}
	after, err := e.store.GetApplicationStandardEnrollment(t.Context(), org.ID, app.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("disabled gate changed intent: %+v %v", after, err)
	}
	xs, err := e.store.ListApplicationStandardExceptions(t.Context(), org.ID, app.ID, "")
	if err != nil || len(xs) != 0 {
		t.Fatalf("disabled gate approved exceptions: %+v %v", xs, err)
	}
}

func TestApplicationStandardMutationCurrentAuthorityBeforeReplay(t *testing.T) {
	for _, action := range []string{"local", "approve"} {
		t.Run(action, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			e.s.WithApplicationStandardMutationsEnabled(true)
			org, app := standardEnrollmentAPIFixture(t.Context(), t, e)
			installStandardMutationFixture(t, e)
			actor, err := e.store.CreateAccount(t.Context(), "mutation-admin@example.com", api.PlanFree)
			if err != nil {
				t.Fatal(err)
			}
			if err := e.store.AddOrgMember(t.Context(), org.ID, actor.ID, state.OrgRoleAdmin, nil); err != nil {
				t.Fatal(err)
			}
			read := standardAPIReader(t, e, actor, []string{api.ScopeDeployWrite})
			method, path, body := http.MethodPut, "/v1/orgs/"+org.Slug+"/application-standard-enrollments/"+app.ID+"/local-intent", any(api.SetApplicationStandardLocalIntentRequest{ExpectedRevision: 1, Settings: json.RawMessage(`{}`), AdditionalLogDestinations: []string{}})
			want := http.StatusOK
			if action == "approve" {
				method, path, body, want = http.MethodPost, strings.TrimSuffix(path, "/local-intent")+"/exceptions", standardMutationExceptionRequest(t, e, org, 1), http.StatusCreated
			}
			headers := map[string]string{"Idempotency-Key": "mutation-replay"}
			first := read.do(t, method, path, body, headers)
			if first.Code != want {
				t.Fatalf("first mutation: %d %s", first.Code, first.Body)
			}
			assertStandardMutationResponsePrivate(t, first.Body.String())
			second := read.do(t, method, path, body, headers)
			if second.Code != want || second.Body.String() != first.Body.String() {
				t.Fatalf("replay: %d %s", second.Code, second.Body)
			}
			e.s.WithApplicationStandardMutationsEnabled(false)
			assertProblem(t, read.do(t, method, path, body, headers), http.StatusServiceUnavailable, api.CodeApplicationStandardsPending)
			e.s.WithApplicationStandardMutationsEnabled(true)
			if err := e.store.UpdateOrgMemberRole(t.Context(), org.ID, actor.ID, state.OrgRoleViewer); err != nil {
				t.Fatal(err)
			}
			assertProblem(t, read.do(t, method, path, body, headers), http.StatusForbidden, api.CodeOrgRoleForbidden)
		})
	}
}

func TestApplicationStandardMutationRoleAndScope(t *testing.T) {
	for _, role := range []state.OrgRole{state.OrgRoleAdmin, state.OrgRoleDeveloper, state.OrgRoleViewer, state.OrgRoleBilling} {
		t.Run(string(role), func(t *testing.T) {
			e := setup(t, api.PlanPro)
			e.s.WithApplicationStandardMutationsEnabled(true)
			org, app := standardEnrollmentAPIFixture(t.Context(), t, e)
			installStandardMutationFixture(t, e)
			actor, err := e.store.CreateAccount(t.Context(), "mutation-role@example.com", api.PlanFree)
			if err != nil {
				t.Fatal(err)
			}
			if err := e.store.AddOrgMember(t.Context(), org.ID, actor.ID, role, nil); err != nil {
				t.Fatal(err)
			}
			read := standardAPIReader(t, e, actor, []string{api.ScopeDeployWrite})
			base := "/v1/orgs/" + org.Slug + "/application-standard-enrollments/" + app.ID
			local := api.SetApplicationStandardLocalIntentRequest{ExpectedRevision: 1, Settings: json.RawMessage(`{}`), AdditionalLogDestinations: []string{}}
			got := read.do(t, http.MethodPut, base+"/local-intent", local, nil)
			want := http.StatusForbidden
			if role == state.OrgRoleAdmin || role == state.OrgRoleDeveloper {
				want = http.StatusOK
			}
			if got.Code != want {
				t.Fatalf("local role %s: %d %s", role, got.Code, got.Body)
			}
			approval := standardMutationExceptionRequest(t, e, org, 1)
			got = read.do(t, http.MethodPost, base+"/exceptions", approval, nil)
			want = http.StatusForbidden
			if role == state.OrgRoleAdmin {
				want = http.StatusCreated
			}
			if got.Code != want {
				t.Fatalf("approval role %s: %d %s", role, got.Code, got.Body)
			}
			var x api.ApplicationStandardException
			if role == state.OrgRoleAdmin {
				if err := json.Unmarshal(got.Body.Bytes(), &x); err != nil {
					t.Fatal(err)
				}
			} else {
				owner := e.do(t, http.MethodPost, base+"/exceptions", approval, nil)
				if owner.Code != http.StatusCreated {
					t.Fatalf("owner approval: %d %s", owner.Code, owner.Body)
				}
				if err := json.Unmarshal(owner.Body.Bytes(), &x); err != nil {
					t.Fatal(err)
				}
			}
			got = read.do(t, http.MethodPost, base+"/exceptions/"+x.ID+"/revoke", api.RevokeApplicationStandardExceptionRequest{ExpectedRevision: 2}, nil)
			want = http.StatusForbidden
			if role == state.OrgRoleAdmin {
				want = http.StatusOK
			}
			if got.Code != want {
				t.Fatalf("revocation role %s: %d %s", role, got.Code, got.Body)
			}
		})
	}
}

func TestApplicationStandardMutationScopeRefusals(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.WithApplicationStandardMutationsEnabled(true)
	org, app := standardEnrollmentAPIFixture(t.Context(), t, e)
	installStandardMutationFixture(t, e)
	base := "/v1/orgs/" + org.Slug + "/application-standard-enrollments/" + app.ID
	read := standardAPIReader(t, e, e.acct, []string{api.ScopeAppsRead})
	for _, tc := range []struct{ method, path string }{{http.MethodPut, base + "/local-intent"}, {http.MethodPost, base + "/exceptions"}, {http.MethodPost, base + "/exceptions/" + uuid.NewString() + "/revoke"}} {
		assertProblem(t, read.do(t, tc.method, tc.path, json.RawMessage(`{}`), nil), http.StatusForbidden, api.CodeForbidden)
	}
	other := seedSharedOrgWithOwner(t, e, "mutation-foreign", "Foreign", api.PlanPro)
	assertProblem(t, e.do(t, http.MethodPut, "/v1/orgs/"+other.Slug+"/application-standard-enrollments/"+app.ID+"/local-intent", json.RawMessage(`{}`), nil), http.StatusNotFound, api.CodeNotFound)
	for _, bad := range []string{"bad", uuid.Nil.String()} {
		assertProblem(t, e.do(t, http.MethodPost, base+"/exceptions/"+bad+"/revoke", api.RevokeApplicationStandardExceptionRequest{ExpectedRevision: 1}, nil), http.StatusBadRequest, api.CodeValidation)
	}
	assertProblem(t, e.do(t, http.MethodPost, base+"/exceptions/"+uuid.NewString()+"/revoke", api.RevokeApplicationStandardExceptionRequest{ExpectedRevision: 1}, nil), http.StatusNotFound, api.CodeNotFound)
	if _, err := e.store.SoftDeleteAppCascade(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	assertProblem(t, e.do(t, http.MethodPut, base+"/local-intent", json.RawMessage(`{}`), nil), http.StatusNotFound, api.CodeNotFound)
}

func TestApplicationStandardMutationConstraintRefusals(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.WithApplicationStandardMutationsEnabled(true)
	org, app := standardEnrollmentAPIFixture(t.Context(), t, e)
	before := installStandardMutationFixture(t, e)
	base := "/v1/orgs/" + org.Slug + "/application-standard-enrollments/" + app.ID
	for _, change := range []func(*api.ApproveApplicationStandardExceptionRequest){
		func(r *api.ApproveApplicationStandardExceptionRequest) { r.ExpiresAt = time.Now().Add(-time.Second) },
		func(r *api.ApproveApplicationStandardExceptionRequest) {
			r.ExpiresAt = time.Now().Add(api.ApplicationStandardMaxExceptionTTL + time.Hour)
		},
		func(r *api.ApproveApplicationStandardExceptionRequest) {
			r.Field, r.Value = appstandards.EgressExtraPorts, json.RawMessage(`[25]`)
		},
		func(r *api.ApproveApplicationStandardExceptionRequest) { r.Version = 2 },
	} {
		r := standardMutationExceptionRequest(t, e, org, 1)
		change(&r)
		response := e.do(t, http.MethodPost, base+"/exceptions", r, nil)
		assertProblem(t, response, http.StatusBadRequest, api.CodeValidation)
		assertStandardMutationResponsePrivate(t, response.Body.String())
	}
	for _, body := range []json.RawMessage{
		json.RawMessage(`{"expected_revision":1,"settings":{"require_signed":true,"require_signed":false},"additional_log_destinations":[]}`),
		json.RawMessage(`{"expected_revision":1,"settings":{},"additional_log_destinations":[],"observed_revision":1}`),
	} {
		assertProblem(t, e.do(t, http.MethodPut, base+"/local-intent", body, nil), http.StatusBadRequest, api.CodeValidation)
	}
	after, err := e.store.GetApplicationStandardEnrollment(t.Context(), org.ID, app.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("refused mutation changed installed intent: %+v %v", after, err)
	}
	xs, err := e.store.ListApplicationStandardExceptions(t.Context(), org.ID, app.ID, "")
	if err != nil || len(xs) != 0 {
		t.Fatalf("refused approval persisted history: %+v %v", xs, err)
	}
}

func TestApplicationStandardMutationConcurrentRevision(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.WithApplicationStandardMutationsEnabled(true)
	org, app := standardEnrollmentAPIFixture(t.Context(), t, e)
	installStandardMutationFixture(t, e)
	path := "/v1/orgs/" + org.Slug + "/application-standard-enrollments/" + app.ID + "/local-intent"
	codes := make(chan int, 2)
	start := make(chan struct{})
	for _, settings := range []string{`{"egress_cidrs":["8.8.8.8/32"]}`, `{"egress_cidrs":["8.8.8.9/32"]}`} {
		go func() {
			<-start
			codes <- e.do(t, http.MethodPut, path, api.SetApplicationStandardLocalIntentRequest{ExpectedRevision: 1, Settings: json.RawMessage(settings), AdditionalLogDestinations: []string{}}, nil).Code
		}()
	}
	close(start)
	counts := map[int]int{}
	counts[<-codes]++
	counts[<-codes]++
	if counts[http.StatusOK] != 1 || counts[http.StatusConflict] != 1 {
		t.Fatalf("revision admitted concurrent changes: %+v", counts)
	}
	enrollment, err := e.store.GetApplicationStandardEnrollment(t.Context(), org.ID, app.ID)
	if err != nil || enrollment.DesiredRevision != 2 || enrollment.PersistedRevision != 1 || enrollment.ObservedRevision != 0 {
		t.Fatalf("concurrent revision: %+v %v", enrollment, err)
	}
}
