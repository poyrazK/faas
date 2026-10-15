package state

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

type standardLocalIntentTestStore interface {
	standardOperationControlTestStore
	ApplicationStandardAutomaticMaterializationStore
	ApplicationStandardLocalIntentStore
}

type standardLocalIntentFixture struct {
	owner                   CreateAccountWithPersonalOrgResult
	app                     App
	company, extra, rotated ApplicationStandardLogDestination
	publishers              []api.ApplicationStandardPublisher
	version                 ApplicationStandardVersion
	assignmentID            string
	enrollment              ApplicationStandardEnrollment
}

func newStandardLocalIntentFixture(ctx context.Context, t *testing.T, s standardLocalIntentTestStore) standardLocalIntentFixture {
	t.Helper()
	owner, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "local-intent-owner@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	f := standardLocalIntentFixture{owner: owner}
	for _, name := range []string{"company", "extra", "rotated"} {
		d, err := s.CreateApplicationStandardLogDestination(ctx, ApplicationStandardLogDestinationCreate{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Name: name, Kind: "http_json", TargetURL: "https://" + name + ".example.com/logs", AuthHeaderSealed: []byte("sealed-" + name)})
		if err != nil {
			t.Fatal(err)
		}
		switch name {
		case "company":
			f.company = d
		case "extra":
			f.extra = d
		case "rotated":
			f.rotated = d
		}
	}
	for _, name := range []string{"company-a", "company-b"} {
		f.publishers = append(f.publishers, standardLocalIntentPublisher(ctx, t, s, owner, name))
	}
	f.version, err = s.PublishApplicationStandardVersion(ctx, ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "local-intent-baseline", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: standardLocalIntentDefinition(f, f.company.ID)}})
	if err != nil {
		t.Fatal(err)
	}
	f.assignmentID = standardLocalIntentInitialAdmission(ctx, t, s, f)
	f.app, err = s.CreateApp(ctx, App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: "local-intent-service", RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	f.enrollment = standardLocalIntentMaterialize(ctx, t, s, f)
	return f
}

func standardLocalIntentPublisher(ctx context.Context, t *testing.T, s standardLocalIntentTestStore, owner CreateAccountWithPersonalOrgResult, name string) api.ApplicationStandardPublisher {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateApplicationStandardPublisher(ctx, ApplicationStandardPublisherCreate{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Name: name, PublicKeyDER: der})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func standardLocalIntentDefinition(f standardLocalIntentFixture, companyID string) json.RawMessage {
	return mustStandardLocalJSON(appstandards.Definition{
		appstandards.LogDestinations:   {Mode: appstandards.Mandatory, Override: appstandards.Extend, Value: mustStandardLocalJSON([]string{companyID})},
		appstandards.RequireSigned:     {Mode: appstandards.Mandatory, Value: json.RawMessage(`true`)},
		appstandards.SecurityPolicy:    {Mode: appstandards.Mandatory, Override: appstandards.Narrow, Value: json.RawMessage(`"warn"`)},
		appstandards.TrustedPublishers: {Mode: appstandards.Mandatory, Override: appstandards.Narrow, Value: mustStandardLocalJSON([]string{f.publishers[0].ID, f.publishers[1].ID})},
		appstandards.EgressCIDRs:       {Mode: appstandards.Restricted, Value: json.RawMessage(`["8.8.8.0/24"]`)},
		appstandards.EgressExtraPorts:  {Mode: appstandards.Restricted, Value: json.RawMessage(`[8443,9443]`)},
	})
}

func standardLocalIntentInitialAdmission(ctx context.Context, t *testing.T, s standardLocalIntentTestStore, f standardLocalIntentFixture) string {
	t.Helper()
	p, err := s.PreviewApplicationStandardAssignment(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, ApplicationStandardReviewRequest{Scope: "organization", ScopeID: f.owner.PersonalOrg.ID, StandardID: f.version.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("initial admission: %+v %v", p.Blockers, err)
	}
	o, err := s.ApproveApplicationStandardReview(ctx, p.OrgID, f.owner.Account.ID, p.ID, p.ApprovalHash)
	if err != nil || len(o.Targets) != 0 {
		t.Fatalf("empty-scope approval: %+v %v", o, err)
	}
	c, err := s.ClaimApplicationStandardOperation(ctx, "local-intent-empty-scope")
	if err != nil {
		t.Fatal(err)
	}
	o, err = s.MaterializeNextApplicationStandardTarget(ctx, c)
	if err != nil || o.State != "completed" {
		t.Fatalf("empty-scope checkpoint: %+v %v", o, err)
	}
	// There were no service targets, so completion claims no consumer ACK.
	return p.Request.AssignmentID
}

func standardLocalIntentMaterialize(ctx context.Context, t *testing.T, s standardLocalIntentTestStore, f standardLocalIntentFixture) ApplicationStandardEnrollment {
	t.Helper()
	c, err := s.ClaimApplicationStandardEnrollment(ctx, "local-intent-automatic-worker")
	if err != nil || !sameStandardUUID(c.AppID, f.app.ID) {
		t.Fatalf("automatic claim: %+v %v", c, err)
	}
	e, err := s.MaterializeApplicationStandardEnrollment(ctx, c)
	if err != nil || e.State != "persisted" || e.PersistedRevision != e.DesiredRevision || e.ObservedRevision != 0 {
		t.Fatalf("automatic materialization: %+v %v", e, err)
	}
	return e
}

func TestMemApplicationStandardLocalIntent(t *testing.T) {
	ctx := context.Background()
	standardLocalIntentLifecycle(ctx, t, NewMemStore())
}

func standardLocalIntentLifecycle(ctx context.Context, t *testing.T, s standardLocalIntentTestStore) {
	t.Helper()
	f := newStandardLocalIntentFixture(ctx, t, s)
	r := ApplicationStandardLocalIntentRequest{ExpectedRevision: f.enrollment.DesiredRevision, Settings: mustStandardLocalJSON(appstandards.Settings{appstandards.EgressCIDRs: json.RawMessage(`["8.8.8.8/32"]`), appstandards.EgressExtraPorts: json.RawMessage(`[8443]`), appstandards.SecurityPolicy: json.RawMessage(`"enforce"`), appstandards.TrustedPublishers: mustStandardLocalJSON([]string{f.publishers[0].ID})}), AdditionalLogDestinations: []string{f.extra.ID}}
	e, err := s.SetApplicationStandardLocalIntent(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, r)
	if err != nil || e.DesiredRevision != r.ExpectedRevision+1 || e.PersistedRevision != f.enrollment.PersistedRevision || e.ObservedRevision != 0 || e.State != "pending" {
		t.Fatalf("pending local intent: %+v %v", e, err)
	}
	app, err := s.AppByID(ctx, f.app.ID)
	if err != nil || app.SecurityPolicy != api.AppSecurityPolicyWarn || len(app.EgressPorts) != 2 {
		t.Fatalf("intent write changed controls before materialization: %+v %v", app, err)
	}
	if _, err := s.CreateDeployment(ctx, Deployment{AppID: f.app.ID, Kind: DeploymentKindImage, Status: DeployPending}); !errors.Is(err, ErrApplicationStandardsPending) {
		t.Fatalf("pending override bypassed deployment gate: %v", err)
	}
	if _, err := s.SetApplicationStandardLocalIntent(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, r); !errors.Is(err, ErrApplicationStandardLocalIntentStale) {
		t.Fatalf("stale intent accepted: %v", err)
	}
	f.enrollment = standardLocalIntentMaterialize(ctx, t, s, f)
	app, err = s.AppByID(ctx, f.app.ID)
	if err != nil || app.SecurityPolicy != api.AppSecurityPolicyEnforce || len(app.EgressPorts) != 1 || app.EgressPorts[0] != 8443 || len(app.EgressAllowlist) != 1 || app.EgressAllowlist[0].String() != "8.8.8.8/32" {
		t.Fatalf("local controls not installed: %+v %v", app, err)
	}
	standardLocalIntentLogTargets(ctx, t, s, f, []string{f.company.TargetURL, f.extra.TargetURL})
	standardLocalIntentRotateCompany(ctx, t, s, &f)
	standardLocalIntentClear(ctx, t, s, f)
}

func standardLocalIntentLogTargets(ctx context.Context, t *testing.T, s standardLocalIntentTestStore, f standardLocalIntentFixture, want []string) {
	t.Helper()
	drains, err := s.ListAppLogDrainsForApp(ctx, f.app.ID)
	if err != nil || len(drains) != len(want) {
		t.Fatalf("logging targets: %+v %v", drains, err)
	}
	for _, target := range want {
		found := false
		for _, drain := range drains {
			if drain.TargetURL == target {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing destination %s", target)
		}
	}
}

func standardLocalIntentRotateCompany(ctx context.Context, t *testing.T, s standardLocalIntentTestStore, f *standardLocalIntentFixture) {
	t.Helper()
	_, err := s.PublishApplicationStandardVersion(ctx, ApplicationStandardPublish{OrgID: f.owner.PersonalOrg.ID, ActorID: f.owner.Account.ID, Slug: f.version.Slug, CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{ExpectedVersion: 1, Definition: standardLocalIntentDefinition(*f, f.rotated.ID)}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewApplicationStandardAssignment(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, ApplicationStandardReviewRequest{AssignmentID: f.assignmentID, ExpectedRevision: 1, Scope: "organization", ScopeID: f.owner.PersonalOrg.ID, StandardID: f.version.StandardID, AdmissionVersion: 2, Active: true, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("company rotation preview: %+v %v", p.Blockers, err)
	}
	o, err := s.ApproveApplicationStandardReview(ctx, p.OrgID, f.owner.Account.ID, p.ID, p.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardOperation(ctx, "local-intent-company-rotation")
	if err != nil {
		t.Fatal(err)
	}
	o, err = s.MaterializeNextApplicationStandardTarget(ctx, c)
	if err != nil || o.State != "waiting" {
		t.Fatalf("rotation materialization: %+v %v", o, err)
	}
	standardLocalIntentLogTargets(ctx, t, s, *f, []string{f.rotated.TargetURL, f.extra.TargetURL})
	f.enrollment, err = s.GetApplicationStandardEnrollment(ctx, p.OrgID, f.app.ID)
	if err != nil || len(f.enrollment.AdditionalLogDestinations) != 1 || f.enrollment.AdditionalLogDestinations[0] != f.extra.ID {
		t.Fatalf("company destination became a permanent extra: %+v %v", f.enrollment, err)
	}
	r := ApplicationStandardLocalIntentRequest{ExpectedRevision: f.enrollment.DesiredRevision, Settings: json.RawMessage(`{}`)}
	if _, err := s.SetApplicationStandardLocalIntent(ctx, p.OrgID, f.owner.Account.ID, f.app.ID, r); !errors.Is(err, ErrApplicationStandardOperationInProgress) {
		t.Fatalf("local intent changed during active rollout: %v", err)
	}
	if _, err := s.ControlApplicationStandardOperation(ctx, p.OrgID, f.owner.Account.ID, o.ID, o.UpdatedAt, ApplicationStandardOperationAbort); err != nil {
		t.Fatal(err)
	}
}

func standardLocalIntentClear(ctx context.Context, t *testing.T, s standardLocalIntentTestStore, f standardLocalIntentFixture) {
	t.Helper()
	r := ApplicationStandardLocalIntentRequest{ExpectedRevision: f.enrollment.DesiredRevision, Settings: json.RawMessage(`{}`)}
	if _, err := s.SetApplicationStandardLocalIntent(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, r); err != nil {
		t.Fatal(err)
	}
	f.enrollment = standardLocalIntentMaterialize(ctx, t, s, f)
	if len(f.enrollment.LocalSettings) != 0 || len(f.enrollment.AdditionalLogDestinations) != 0 {
		t.Fatalf("clear did not restore inheritance: %+v", f.enrollment)
	}
	app, err := s.AppByID(ctx, f.app.ID)
	if err != nil || app.SecurityPolicy != api.AppSecurityPolicyWarn || len(app.EgressPorts) != 2 || app.EgressAllowlist[0].String() != "8.8.8.0/24" {
		t.Fatalf("inherited controls: %+v %v", app, err)
	}
	standardLocalIntentLogTargets(ctx, t, s, f, []string{f.rotated.TargetURL})
	r.ExpectedRevision = f.enrollment.DesiredRevision
	before := f.enrollment
	got, err := s.SetApplicationStandardLocalIntent(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, r)
	if err != nil || got.DesiredRevision != before.DesiredRevision || !got.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("same local intent was rewritten: %+v %v", got, err)
	}
}

func TestMemApplicationStandardLocalIntentRefusals(t *testing.T) {
	ctx := context.Background()
	standardLocalIntentRefusals(ctx, t, NewMemStore())
}

func standardLocalIntentRefusals(ctx context.Context, t *testing.T, s standardLocalIntentTestStore) {
	t.Helper()
	f := newStandardLocalIntentFixture(ctx, t, s)
	for _, raw := range []string{`{"require_signed":false}`, `{"egress_cidrs":[]}`, `{"egress_cidrs":["1.1.1.1/32"]}`, `{"egress_extra_ports":[5432]}`, `{"security_policy":"off"}`} {
		t.Run(raw, func(t *testing.T) {
			_, err := s.SetApplicationStandardLocalIntent(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, ApplicationStandardLocalIntentRequest{ExpectedRevision: f.enrollment.DesiredRevision, Settings: json.RawMessage(raw)})
			if !errors.Is(err, ErrApplicationStandardReviewBlocked) {
				t.Fatalf("weakened requirement accepted: %v", err)
			}
		})
	}
	for _, id := range []string{f.company.ID, "46bff5b0-a996-488b-bbf3-8c5b491947b7"} {
		_, err := s.SetApplicationStandardLocalIntent(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, ApplicationStandardLocalIntentRequest{ExpectedRevision: f.enrollment.DesiredRevision, Settings: json.RawMessage(`{}`), AdditionalLogDestinations: []string{id}})
		if err == nil {
			t.Fatal("enrolled required or unknown extra destination")
		}
	}
	got, err := s.GetApplicationStandardEnrollment(ctx, f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || got.DesiredRevision != f.enrollment.DesiredRevision || !got.UpdatedAt.Equal(f.enrollment.UpdatedAt) {
		t.Fatalf("refused intent changed enrollment: %+v %v", got, err)
	}
	logs, err := s.ListAuditLog(ctx, AuditLogFilter{KindPrefix: "application_standard.local_intent_changed", IncludeAnonymous: true, Limit: 100})
	if err != nil || len(logs) != 0 {
		t.Fatalf("refused intent wrote audit: %+v %v", logs, err)
	}
	standardLocalIntentRoles(ctx, t, s, f)
}

func standardLocalIntentRoles(ctx context.Context, t *testing.T, s standardLocalIntentTestStore, f standardLocalIntentFixture) {
	t.Helper()
	other, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "local-intent-member@example.com", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	r := ApplicationStandardLocalIntentRequest{ExpectedRevision: f.enrollment.DesiredRevision, Settings: json.RawMessage(`{"egress_extra_ports":[8443]}`)}
	for _, role := range []OrgRole{OrgRoleViewer, OrgRoleBilling, OrgRoleDeveloper} {
		if role == OrgRoleViewer {
			err = s.AddOrgMember(ctx, f.owner.PersonalOrg.ID, other.Account.ID, role, nil)
		} else {
			err = s.UpdateOrgMemberRole(ctx, f.owner.PersonalOrg.ID, other.Account.ID, role)
		}
		if err != nil {
			t.Fatal(err)
		}
		e, err := s.SetApplicationStandardLocalIntent(ctx, f.owner.PersonalOrg.ID, other.Account.ID, f.app.ID, r)
		if role == OrgRoleDeveloper {
			if err != nil || e.State != "pending" {
				t.Fatalf("developer permitted change: %+v %v", e, err)
			}
		} else if !errors.Is(err, ErrApplicationStandardReviewForbidden) {
			t.Fatalf("%s changed local intent: %v", role, err)
		}
	}
	if _, err := s.SetApplicationStandardLocalIntent(ctx, other.PersonalOrg.ID, other.Account.ID, f.app.ID, r); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-organization local intent: %v", err)
	}
}

func TestMemApplicationStandardLocalIntentConcurrent(t *testing.T) {
	standardLocalIntentConcurrent(t.Context(), t, NewMemStore())
}

func standardLocalIntentConcurrent(ctx context.Context, t *testing.T, s standardLocalIntentTestStore) {
	t.Helper()
	f := newStandardLocalIntentFixture(ctx, t, s)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, raw := range []string{`{"egress_extra_ports":[8443]}`, `{"egress_extra_ports":[9443]}`} {
		wg.Add(1)
		go func(raw string) {
			defer wg.Done()
			<-start
			_, err := s.SetApplicationStandardLocalIntent(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, ApplicationStandardLocalIntentRequest{ExpectedRevision: f.enrollment.DesiredRevision, Settings: json.RawMessage(raw)})
			results <- err
		}(raw)
	}
	close(start)
	wg.Wait()
	close(results)
	writes := 0
	for err := range results {
		if err == nil {
			writes++
		} else if !errors.Is(err, ErrApplicationStandardLocalIntentStale) && !errors.Is(err, ErrApplicationStandardReviewBusy) {
			t.Fatal("unexpected competing intent outcome", err)
		}
	}
	e, err := s.GetApplicationStandardEnrollment(ctx, f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || writes != 1 || e.DesiredRevision != f.enrollment.DesiredRevision+1 || e.PersistedRevision != f.enrollment.PersistedRevision || e.State != "pending" {
		t.Fatalf("competing intent writes=%d enrollment=%+v err=%v", writes, e, err)
	}
	logs, err := s.ListAuditLog(ctx, AuditLogFilter{KindPrefix: "application_standard.local_intent_changed", IncludeAnonymous: true, Limit: 100})
	if err != nil || len(logs) != 1 {
		t.Fatalf("competing intent audits: %+v %v", logs, err)
	}
}
