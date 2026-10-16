package state

// Portable authentic signatures and composed-scan fixtures; no native ACKs.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

type standardLocalArtifactTestStore interface {
	standardLocalIntentTestStore
	standardArtifactReviewTestStore
}

func TestMemApplicationStandardLocalIntentArtifacts(t *testing.T) {
	standardLocalIntentArtifacts(t, func(*testing.T) standardLocalArtifactTestStore { return NewMemStore() })
}

func TestPgApplicationStandardLocalIntentArtifacts(t *testing.T) {
	standardLocalIntentArtifacts(t, func(t *testing.T) standardLocalArtifactTestStore { s, _ := standardOperationPGStore(t); return s })
}

func standardLocalIntentArtifacts(t *testing.T, newStore func(*testing.T) standardLocalArtifactTestStore) {
	t.Helper()
	for _, kind := range []string{"source", "function", "registry"} {
		for _, change := range []string{"verified", "unsafe-rescan", "different-publisher", "rescan-after-request"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				standardLocalArtifactChange(t, newStore(t), kind, change)
			})
		}
	}
}

func standardLocalArtifactChange(t *testing.T, s standardLocalArtifactTestStore, kind, change string) {
	t.Helper()
	app, dep, other := standardLocalArtifactBaseline(t, s, kind)
	e, err := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings := json.RawMessage(`{"security_policy":"enforce"}`)
	code := ""
	if change == "unsafe-rescan" {
		publishStandardReviewScan(t, s, app, dep, true)
		code = "artifact_security_policy_conflict"
	}
	if change == "different-publisher" {
		settings = mustStandardLocalJSON(appstandards.Settings{appstandards.TrustedPublishers: mustStandardLocalJSON([]string{other.ID})})
		code = "artifact_publisher_not_approved"
	}
	got, err := s.SetApplicationStandardLocalIntent(t.Context(), app.OrgID, app.AccountID, app.ID, ApplicationStandardLocalIntentRequest{ExpectedRevision: e.DesiredRevision, Settings: settings})
	if code != "" {
		if !errors.Is(err, ErrApplicationStandardReviewBlocked) || !strings.Contains(err.Error(), code) {
			t.Fatalf("current artifact gate %s: %v", code, err)
		}
		fresh, readErr := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
		if readErr != nil || fresh.DesiredRevision != e.DesiredRevision || fresh.State != e.State {
			t.Fatalf("artifact refusal changed intent: %+v %v", fresh, readErr)
		}
		return
	}
	if err != nil || got.State != "pending" || got.ObservedRevision != 0 {
		t.Fatalf("verified artifact local intent: %+v %v", got, err)
	}
	standardLocalArtifactInstall(t, s, app, dep, got, change)
}

func standardLocalArtifactInstall(t *testing.T, s standardLocalArtifactTestStore, app App, dep Deployment, pending ApplicationStandardEnrollment, change string) {
	t.Helper()
	if change == "rescan-after-request" {
		publishStandardReviewScan(t, s, app, dep, true)
	}
	c, err := s.ClaimApplicationStandardEnrollment(t.Context(), "local-artifact-worker")
	if err != nil || c.DesiredRevision != pending.DesiredRevision {
		t.Fatalf("artifact local intent claim: %+v %v", c, err)
	}
	e, err := s.MaterializeApplicationStandardEnrollment(t.Context(), c)
	actual, readErr := s.AppByID(t.Context(), app.ID)
	if change == "rescan-after-request" {
		if err != nil || readErr != nil || e.State != "blocked" || e.ErrorCode != "artifact_security_policy_conflict" || e.PersistedRevision != pending.PersistedRevision || actual.SecurityPolicy != api.AppSecurityPolicyWarn || e.ObservedRevision != 0 {
			t.Fatalf("unsafe rescan bypassed worker fence: %+v %+v %v %v", e, actual, err, readErr)
		}
		return
	}
	if err != nil || readErr != nil || e.State != "persisted" || e.PersistedRevision != pending.DesiredRevision || e.ObservedRevision != 0 || actual.SecurityPolicy != api.AppSecurityPolicyEnforce {
		t.Fatalf("verified local policy not installed: %+v %+v %v %v", e, actual, err, readErr)
	}
}

func standardLocalArtifactFixture(t *testing.T, s standardLocalArtifactTestStore, kind string) (App, Deployment) {
	t.Helper()
	var app App
	var dep Deployment
	var root SourceBuildRootfsInput
	if kind == "registry" {
		_, _, app, dep = artifactScanBaseFixture(t, s)
	} else {
		runtime := ""
		if kind == "function" {
			runtime = "node22"
		}
		f := sourceBuildRootfsFixtureRuntime(t, s, runtime)
		app, dep, root = f.App, f.Dep, f.Input
	}
	signed, policy := true, api.AppSecurityPolicyWarn
	var err error
	app, err = s.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetRequireSigned: true, RequireSigned: &signed, SetSecurityPolicy: true, SecurityPolicy: &policy})
	if err != nil {
		t.Fatal(err)
	}
	if kind != "registry" {
		root.IntentHash, err = SourceBuildRootfsIntentHash(app, dep)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.PublishSourceBuildRootfs(t.Context(), root); err != nil {
			t.Fatal(err)
		}
	}
	publishStandardReviewScan(t, s, app, dep, false)
	return app, dep
}

func standardLocalArtifactBaseline(t *testing.T, s standardLocalArtifactTestStore, kind string) (App, Deployment, api.ApplicationStandardPublisher) {
	t.Helper()
	app, dep := standardLocalArtifactFixture(t, s, kind)
	keys, err := s.ListAppTrustedSignersForApp(t.Context(), app.ID)
	if err != nil || len(keys) != 1 {
		t.Fatalf("artifact publisher: %+v %v", keys, err)
	}
	current, err := s.CreateApplicationStandardPublisher(t.Context(), ApplicationStandardPublisherCreate{OrgID: app.OrgID, ActorID: app.AccountID, Name: "Current signed publisher", PublicKeyDER: keys[0].CosignPublicKey})
	if err != nil {
		t.Fatal(err)
	}
	other := standardLocalIntentPublisher(t.Context(), t, s, CreateAccountWithPersonalOrgResult{Account: Account{ID: app.AccountID}, PersonalOrg: Org{ID: app.OrgID}}, "Other approved publisher")
	definition := mustStandardLocalJSON(appstandards.Definition{
		appstandards.RequireSigned:     {Mode: appstandards.Mandatory, Value: json.RawMessage(`true`)},
		appstandards.TrustedPublishers: {Mode: appstandards.Mandatory, Override: appstandards.Narrow, Value: mustStandardLocalJSON([]string{current.ID, other.ID})},
		appstandards.SecurityPolicy:    {Mode: appstandards.Mandatory, Override: appstandards.Narrow, Value: json.RawMessage(`"warn"`)},
	})
	v, err := s.PublishApplicationStandardVersion(t.Context(), ApplicationStandardPublish{OrgID: app.OrgID, ActorID: app.AccountID, Slug: "local-artifact-" + uuid.NewString()[:8], CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: definition}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewApplicationStandardAssignment(t.Context(), app.OrgID, app.AccountID, ApplicationStandardReviewRequest{Scope: "application", ScopeID: app.ID, StandardID: v.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("artifact baseline review: %+v %v", p.Blockers, err)
	}
	o, err := s.ApproveApplicationStandardReview(t.Context(), app.OrgID, app.AccountID, p.ID, p.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardOperation(t.Context(), "local-artifact-baseline-worker")
	if err != nil {
		t.Fatal(err)
	}
	o, err = s.MaterializeNextApplicationStandardTarget(t.Context(), c)
	if err != nil || o.State != "waiting" {
		t.Fatalf("artifact baseline installation: %+v %v", o, err)
	}
	// End the fixture operation without inventing consumer observation.
	if _, err := s.ControlApplicationStandardOperation(t.Context(), app.OrgID, app.AccountID, o.ID, o.UpdatedAt, ApplicationStandardOperationAbort); err != nil {
		t.Fatal(err)
	}
	return app, dep, other
}
