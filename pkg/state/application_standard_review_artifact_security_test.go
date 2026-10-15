package state

// adr: 435. Authentic signatures and portable composed-scan fixtures. These
// tests confer no native consumed-byte, rollout ACK or scanner acceptance.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

type standardArtifactReviewTestStore interface {
	standardMaterializationTestStore
	sourceRootfsTestStore
	runtimeScanTestStore
}

func TestMemApplicationStandardArtifactSecurityReview(t *testing.T) {
	standardArtifactSecurityReview(t, func(*testing.T) standardArtifactReviewTestStore { return NewMemStore() })
}

func standardArtifactReviewFixture(t *testing.T, s standardArtifactReviewTestStore, kind string, scan, unsafe bool) (App, Deployment) {
	t.Helper()
	var app App
	var dep Deployment
	if kind == "registry" {
		_, _, app, dep = artifactScanBaseFixture(t, s)
	} else {
		runtime := ""
		if kind == "function" {
			runtime = "node22"
		}
		f := sourceBuildRootfsFixtureRuntime(t, s, runtime)
		if _, err := s.PublishSourceBuildRootfs(t.Context(), f.Input); err != nil {
			t.Fatal(err)
		}
		app, dep = f.App, f.Dep
	}
	if scan {
		publishStandardReviewScan(t, s, app, dep, unsafe)
	}
	return app, dep
}

func publishStandardReviewScan(t *testing.T, s standardArtifactReviewTestStore, app App, dep Deployment, unsafe bool) DeploymentRuntimeScan {
	t.Helper()
	inputs, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := runtimeScanInputFixture(t, inputs)
	if !unsafe {
		for i := range in.Reports {
			in.Reports[i].Report.SeverityCounts = api.SeverityCounts{}
			in.Reports[i].Report.Vulnerabilities = []api.Vulnerability{}
		}
	}
	v, err := s.PublishDeploymentRuntimeScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func standardArtifactReviewRequest(t *testing.T, s standardArtifactReviewTestStore, app App, otherKey bool) ApplicationStandardReviewRequest {
	t.Helper()
	keys, err := s.ListAppTrustedSignersForApp(t.Context(), app.ID)
	if err != nil || len(keys) != 1 {
		t.Fatal("fixture publisher missing", err)
	}
	der := keys[0].CosignPublicKey
	if otherKey {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		der, err = x509.MarshalPKIXPublicKey(&key.PublicKey)
		if err != nil {
			t.Fatal(err)
		}
	}
	publisher, err := s.CreateApplicationStandardPublisher(t.Context(), ApplicationStandardPublisherCreate{OrgID: app.OrgID, ActorID: app.AccountID, Name: "Reviewed company publisher", PublicKeyDER: der})
	if err != nil {
		t.Fatal(err)
	}
	definition := mustMaterializationJSON(t, appstandards.Definition{
		appstandards.RequireSigned:     {Mode: appstandards.Mandatory, Value: json.RawMessage(`true`)},
		appstandards.TrustedPublishers: {Mode: appstandards.Mandatory, Value: mustMaterializationJSON(t, []string{publisher.ID})},
		appstandards.SecurityPolicy:    {Mode: appstandards.Mandatory, Value: json.RawMessage(`"enforce"`)},
	})
	v, err := s.PublishApplicationStandardVersion(t.Context(), ApplicationStandardPublish{OrgID: app.OrgID, ActorID: app.AccountID, Slug: "review-security-" + uuid.NewString()[:8], CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: definition}})
	if err != nil {
		t.Fatal(err)
	}
	return ApplicationStandardReviewRequest{Scope: "application", ScopeID: app.ID, StandardID: v.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1}
}

func standardArtifactSecurityReview(t *testing.T, newStore func(*testing.T) standardArtifactReviewTestStore) {
	t.Helper()
	for _, kind := range []string{"source", "function", "registry"} {
		t.Run(kind, func(t *testing.T) {
			s := newStore(t)
			app, dep := standardArtifactReviewFixture(t, s, kind, true, false)
			r := standardArtifactReviewRequest(t, s, app, false)
			p, err := s.PreviewApplicationStandardAssignment(t.Context(), app.OrgID, app.AccountID, r)
			if err != nil || len(p.Blockers) != 0 {
				t.Fatal("verified current artifact remained blocked", p.Blockers, err)
			}
			evidence, err := s.GetFreshDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID)
			if err != nil || p.ExpiresAt.After(evidence.ExpiresAt) {
				t.Fatal("review outlived scan/publisher authority", err)
			}
			o, err := s.ApproveApplicationStandardReview(t.Context(), app.OrgID, app.AccountID, p.ID, p.ApprovalHash)
			if err != nil {
				t.Fatal("typed artifact approval failed", err)
			}
			c, err := s.ClaimApplicationStandardOperation(t.Context(), "security-review-worker")
			if err != nil || c.OperationID != o.ID {
				t.Fatal(err)
			}
			if _, err := s.MaterializeNextApplicationStandardTarget(t.Context(), c); err != nil {
				t.Fatal("reviewed image policy could not be materialized", err)
			}
			actual, err := s.AppByID(t.Context(), app.ID)
			if err != nil || !actual.RequireSigned || actual.SecurityPolicy != api.AppSecurityPolicyEnforce {
				t.Fatal("review did not install real application policy", err)
			}
		})
	}
}

func TestMemApplicationStandardArtifactSecurityBlockers(t *testing.T) {
	standardArtifactSecurityBlockers(t, NewMemStore())
}

func standardArtifactSecurityBlockers(t *testing.T, s standardArtifactReviewTestStore) {
	t.Helper()
	for _, tc := range []struct {
		name, code             string
		scan, unsafe, otherKey bool
	}{
		{"missing", "current_artifact_verification_required", false, false, false},
		{"unapproved", "artifact_publisher_not_approved", true, false, true},
		{"findings", "artifact_security_policy_conflict", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, _ := standardArtifactReviewFixture(t, s, "function", tc.scan, tc.unsafe)
			r := standardArtifactReviewRequest(t, s, app, tc.otherKey)
			p, err := s.PreviewApplicationStandardAssignment(t.Context(), app.OrgID, app.AccountID, r)
			if err != nil || len(p.Blockers) != 1 || p.Blockers[0].Code != tc.code {
				t.Fatal("unsafe image policy review was not explained", p.Blockers, err)
			}
			if _, err := s.ApproveApplicationStandardReview(t.Context(), app.OrgID, app.AccountID, p.ID, p.ApprovalHash); !errors.Is(err, ErrApplicationStandardReviewBlocked) {
				t.Fatal("blocked policy acquired approval", err)
			}
		})
	}
}

func TestMemApplicationStandardArtifactReviewRescanInvalidatesApproval(t *testing.T) {
	standardArtifactReviewRescan(t, NewMemStore())
}

func standardArtifactReviewRescan(t *testing.T, s standardArtifactReviewTestStore) {
	t.Helper()
	app, dep := standardArtifactReviewFixture(t, s, "source", true, false)
	r := standardArtifactReviewRequest(t, s, app, false)
	p, err := s.PreviewApplicationStandardAssignment(t.Context(), app.OrgID, app.AccountID, r)
	if err != nil || len(p.Blockers) != 0 {
		t.Fatal(p.Blockers, err)
	}
	publishStandardReviewScan(t, s, app, dep, true)
	if _, err := s.ValidateApplicationStandardReview(t.Context(), app.OrgID, app.AccountID, p.ID, p.ApprovalHash); !errors.Is(err, ErrApplicationStandardReviewStale) {
		t.Fatal("changed selected scan retained old review authority", err)
	}
	if _, err := s.ApproveApplicationStandardReview(t.Context(), app.OrgID, app.AccountID, p.ID, p.ApprovalHash); !errors.Is(err, ErrApplicationStandardReviewStale) {
		t.Fatal("changed selected scan acquired old approval", err)
	}
}
