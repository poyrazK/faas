package state

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

type standardMaterializationTestStore interface {
	standardOperationTestStore
	ApplicationStandardMaterializationStore
}

func TestMemApplicationStandardMaterialization(t *testing.T) {
	m := NewMemStore()
	standardMaterializationLifecycle(t, m, func(id string) {
		m.mu.Lock()
		defer m.mu.Unlock()
		o := m.applicationStandardOperations[id]
		o.State = "completed"
		m.applicationStandardOperations[id] = o
	})
}

func standardMaterializationLifecycle(t *testing.T, s standardMaterializationTestStore, finishFixture func(string)) {
	t.Helper()
	ctx := context.Background()
	f := newStandardApprovalFixture(t, s)
	legacy := []AppLogDrain{}
	for _, app := range f.apps {
		d, err := s.CreateAppLogDrain(ctx, AppLogDrain{AppID: app.ID, AccountID: app.AccountID, Kind: AppLogDrainKindHTTPJSON, TargetURL: "https://legacy.example.com/events", AuthHeaderSealed: []byte("sealed-legacy-ciphertext"), Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		legacy = append(legacy, d)
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.UpsertAppTrustedSigner(ctx, app.AccountID, app.ID, "legacy-ci", der, app.AccountID); err != nil {
			t.Fatal(err)
		}
	}
	destination, err := s.CreateApplicationStandardLogDestination(ctx, ApplicationStandardLogDestinationCreate{OrgID: f.owner.PersonalOrg.ID, ActorID: f.owner.Account.ID, Name: "Company logging", Kind: "http_json", TargetURL: "https://company.example.com/logs", AuthHeaderSealed: []byte("sealed-company-ciphertext")})
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := s.CreateApplicationStandardPublisher(ctx, ApplicationStandardPublisherCreate{OrgID: f.owner.PersonalOrg.ID, ActorID: f.owner.Account.ID, Name: "Company CI", PublicKeyDER: der})
	if err != nil {
		t.Fatal(err)
	}
	definition := appstandards.Definition{
		appstandards.LogDestinations:   {Mode: appstandards.Mandatory, Value: mustMaterializationJSON(t, []string{destination.ID})},
		appstandards.TrustedPublishers: {Mode: appstandards.Mandatory, Value: mustMaterializationJSON(t, []string{publisher.ID})},
		appstandards.RequireSigned:     {Mode: appstandards.Mandatory, Value: json.RawMessage(`true`)},
		appstandards.SecurityPolicy:    {Mode: appstandards.Mandatory, Value: json.RawMessage(`"warn"`)},
		appstandards.EgressCIDRs:       {Mode: appstandards.Mandatory, Value: json.RawMessage(`["8.8.8.0/24"]`)},
		appstandards.EgressExtraPorts:  {Mode: appstandards.Mandatory, Value: json.RawMessage(`[8443]`)},
	}
	version, err := s.PublishApplicationStandardVersion(ctx, ApplicationStandardPublish{OrgID: f.owner.PersonalOrg.ID, ActorID: f.owner.Account.ID, Slug: "materialization-all-controls", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: mustMaterializationJSON(t, definition)}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PreviewApplicationStandardAssignment(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, ApplicationStandardReviewRequest{Scope: "organization", ScopeID: f.owner.PersonalOrg.ID, StandardID: version.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 2})
	if err != nil || len(plan.Blockers) != 0 {
		t.Fatalf("projection review: %v %+v", err, plan.Blockers)
	}
	operation, err := s.ApproveApplicationStandardReview(ctx, plan.OrgID, f.owner.Account.ID, plan.ID, plan.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimApplicationStandardOperation(ctx, "projection-worker-a")
	if err != nil || claim.OperationID != operation.ID {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	if _, err := s.ClaimApplicationStandardOperation(ctx, "projection-worker-b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("active lease was stolen: %v", err)
	}
	for range f.apps {
		operation, err = s.MaterializeNextApplicationStandardTarget(ctx, claim)
		if err != nil {
			t.Fatal(err)
		}
	}
	if operation.State != "waiting" || operation.Targets[0].State != "persisted" || operation.Targets[1].State != "persisted" {
		t.Fatalf("persistence was confused with observation: %+v", operation)
	}
	for _, app := range f.apps {
		actual, err := s.AppByID(ctx, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !actual.RequireSigned || actual.SecurityPolicy != api.AppSecurityPolicyWarn || len(actual.EgressAllowlist) != 1 || actual.EgressAllowlist[0] != netip.MustParsePrefix("8.8.8.0/24") || len(actual.EgressPorts) != 1 || actual.EgressPorts[0] != 8443 {
			t.Fatalf("real app controls not installed: %+v", actual)
		}
		e, err := s.GetApplicationStandardEnrollment(ctx, plan.OrgID, app.ID)
		if err != nil || e.PersistedRevision != 2 || e.DesiredRevision != 2 || e.ObservedRevision != 0 || e.State != "persisted" {
			t.Fatalf("revision checkpoint: %+v %v", e, err)
		}
		drains, err := s.ListAppLogDrainsForApp(ctx, app.ID)
		if err != nil || len(drains) != 1 || drains[0].TargetURL != destination.TargetURL || !bytes.Equal(drains[0].AuthHeaderSealed, destination.AuthHeaderSealed) || !drains[0].Enabled {
			t.Fatalf("actual company drain missing: %+v %v", drains, err)
		}
		signers, err := s.ListAppTrustedSignersForApp(ctx, app.ID)
		if err != nil || len(signers) != 1 || signers[0].SignerName != "standard-"+publisher.ID || !bytes.Equal(signers[0].CosignPublicKey, der) {
			t.Fatalf("actual company signer missing: %+v %v", signers, err)
		}
		enabled := true
		if _, err := s.UpdateAppLogDrain(ctx, drains[0].ID, UpdateAppLogDrainParams{Enabled: &enabled}); err != nil {
			t.Fatalf("idempotent managed drain write failed: %v", err)
		}
		if _, _, err := s.UpsertAppTrustedSigner(ctx, app.AccountID, app.ID, signers[0].SignerName, der, app.AccountID); err != nil {
			t.Fatalf("idempotent managed signer write failed: %v", err)
		}
		if err := s.DeleteAppLogDrain(ctx, drains[0].ID); !errors.Is(err, ErrApplicationStandardManagedControl) {
			t.Fatalf("mandatory drain deleted: %v", err)
		}
		disabled := false
		if _, err := s.UpdateAppLogDrain(ctx, drains[0].ID, UpdateAppLogDrainParams{Enabled: &disabled}); !errors.Is(err, ErrApplicationStandardManagedControl) {
			t.Fatalf("mandatory logging disabled: %v", err)
		}
		if err := s.DeleteAppTrustedSigner(ctx, app.AccountID, app.ID, signers[0].SignerName); !errors.Is(err, ErrApplicationStandardManagedControl) {
			t.Fatalf("mandatory signer deleted: %v", err)
		}
		unsigned := false
		if _, err := s.UpdateApp(ctx, app.ID, UpdateAppParams{SetRequireSigned: true, RequireSigned: &unsigned}); !errors.Is(err, ErrApplicationStandardManagedControl) {
			t.Fatalf("signature gate bypassed: %v", err)
		}
	}
	// The review after projection uses organization resource IDs, not physical
	// drain UUIDs or generated signer names. Removed legacy credentials remain
	// available for a future reviewed removal through private sealed backups.
	removal := plan.Request
	removal.AssignmentID, removal.ExpectedRevision, removal.Active = operation.AssignmentID, 1, false
	preview, err := s.PreviewApplicationStandardAssignment(ctx, plan.OrgID, f.owner.Account.ID, removal)
	if err != nil || len(preview.Blockers) != 0 {
		t.Fatalf("restoration preview: %+v %v", preview.Blockers, err)
	}
	for _, reviewed := range preview.Applications {
		if len(standardReviewStrings(reviewed.BeforeSettings[appstandards.LogDestinations])) != 1 || standardReviewStrings(reviewed.BeforeSettings[appstandards.LogDestinations])[0] != destination.ID {
			t.Fatalf("physical drain leaked into inheritance: %+v", reviewed)
		}
		wanted := ""
		for _, d := range legacy {
			if sameStandardUUID(d.AppID, reviewed.AppID) {
				wanted = canonicalStandardUUID(d.ID)
			}
		}
		if standardReviewStrings(reviewed.Effective.Values[appstandards.LogDestinations])[0] != wanted {
			t.Fatalf("legacy restoration intent lost: %+v", reviewed)
		}
	}
	raw, _ := json.Marshal(operation)
	if bytes.Contains(raw, destination.AuthHeaderSealed) || bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString(destination.AuthHeaderSealed))) || bytes.Contains(raw, []byte("sealed-legacy-ciphertext")) {
		t.Fatal("private credentials escaped through operation")
	}
	if _, err := s.MaterializeNextApplicationStandardTarget(ctx, claim); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatalf("released lease remained authoritative: %v", err)
	}

	// Fixture-only completion permits testing the independent reviewed removal
	// transaction. This does not assert that any runtime gate has acknowledged.
	finishFixture(operation.ID)
	removed, err := s.ApproveApplicationStandardReview(ctx, preview.OrgID, f.owner.Account.ID, preview.ID, preview.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimApplicationStandardOperation(ctx, "restoration-worker")
	if err != nil || next.OperationID != removed.ID {
		t.Fatalf("restoration claim: %+v %v", next, err)
	}
	for range f.apps {
		removed, err = s.MaterializeNextApplicationStandardTarget(ctx, next)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, app := range f.apps {
		drains, err := s.ListAppLogDrainsForApp(ctx, app.ID)
		if err != nil || len(drains) != 1 {
			t.Fatalf("legacy drain restoration: %+v %v", drains, err)
		}
		var original AppLogDrain
		for _, d := range legacy {
			if sameStandardUUID(d.AppID, app.ID) {
				original = d
			}
		}
		if !sameStandardUUID(drains[0].ID, original.ID) || drains[0].TargetURL != original.TargetURL || !bytes.Equal(drains[0].AuthHeaderSealed, original.AuthHeaderSealed) || drains[0].Enabled != original.Enabled {
			t.Fatalf("sealed original control was not restored: %+v", drains[0])
		}
		signers, err := s.ListAppTrustedSignersForApp(ctx, app.ID)
		if err != nil || len(signers) != 1 || signers[0].SignerName != "legacy-ci" {
			t.Fatalf("original signer restoration: %+v %v", signers, err)
		}
		actual, err := s.AppByID(ctx, app.ID)
		if err != nil || actual.RequireSigned || actual.SecurityPolicy != api.AppSecurityPolicyOff || len(actual.EgressAllowlist) != 0 || len(actual.EgressPorts) != 0 {
			t.Fatalf("scalar baseline restoration: %+v %v", actual, err)
		}
		e, err := s.GetApplicationStandardEnrollment(ctx, preview.OrgID, app.ID)
		if err != nil || e.ObservedRevision != 0 || e.DesiredRevision != 3 || e.PersistedRevision != 3 {
			t.Fatalf("restoration forged observation: %+v %v", e, err)
		}
	}
}

func mustMaterializationJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMemApplicationStandardMaterializationWaveGate(t *testing.T) {
	standardMaterializationWaveGate(t, NewMemStore())
}
func standardMaterializationWaveGate(t *testing.T, s standardMaterializationTestStore) {
	t.Helper()
	ctx := context.Background()
	f := newStandardApprovalFixture(t, s)
	if _, err := s.ApproveApplicationStandardReview(ctx, f.plan.OrgID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardOperation(ctx, "wave-worker")
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.MaterializeNextApplicationStandardTarget(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if o.State != "waiting" || o.Targets[0].State != "persisted" || o.Targets[1].State != "queued" {
		t.Fatalf("wave crossed before observation: %+v", o)
	}
	c, err = s.ClaimApplicationStandardOperation(ctx, "restarted-worker")
	if err != nil {
		t.Fatal(err)
	}
	o, err = s.MaterializeNextApplicationStandardTarget(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if o.Targets[1].State != "queued" {
		t.Fatalf("restart bypassed convergence gate: %+v", o)
	}
}

func TestMemApplicationStandardMaterializationStale(t *testing.T) {
	standardMaterializationStale(t, NewMemStore())
}
func standardMaterializationStale(t *testing.T, s standardMaterializationTestStore) {
	t.Helper()
	ctx := context.Background()
	f := newStandardApprovalFixture(t, s)
	o, err := s.ApproveApplicationStandardReview(ctx, f.plan.OrgID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	target := o.Targets[0].AppID
	for _, app := range f.apps {
		if sameStandardUUID(app.ID, target) {
			target = app.ID
			break
		}
	}
	signed := true
	if _, err := s.UpdateApp(ctx, target, UpdateAppParams{SetRequireSigned: true, RequireSigned: &signed}); err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardOperation(ctx, "stale-worker")
	if err != nil {
		t.Fatal(err)
	}
	o, err = s.MaterializeNextApplicationStandardTarget(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := s.AppByID(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.GetApplicationStandardEnrollment(ctx, f.plan.OrgID, target)
	if err != nil {
		t.Fatal(err)
	}
	if o.Targets[0].State != "blocked" || o.State != "waiting" || actual.SecurityPolicy != api.AppSecurityPolicyOff || !actual.RequireSigned || e.DesiredRevision != 1 || e.PersistedRevision != 0 {
		t.Fatalf("stale work changed controls/checkpoint: %+v %+v %+v", o, actual, e)
	}
}

func TestMemApplicationStandardMaterializationLeaseFencing(t *testing.T) {
	m := NewMemStore()
	f := newStandardApprovalFixture(t, m)
	ctx := context.Background()
	if _, err := m.ApproveApplicationStandardReview(ctx, f.plan.OrgID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	old, err := m.ClaimApplicationStandardOperation(ctx, "dead-worker")
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	expired := m.applicationStandardWorkerClaims[old.OperationID]
	expired.Until = time.Now().Add(-time.Second)
	m.applicationStandardWorkerClaims[old.OperationID] = expired
	m.mu.Unlock()
	current, err := m.ClaimApplicationStandardOperation(ctx, "replacement-worker")
	if err != nil {
		t.Fatal(err)
	}
	if current.Generation != old.Generation+1 {
		t.Fatal("restart did not advance fencing generation")
	}
	if _, err := m.MaterializeNextApplicationStandardTarget(ctx, old); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatalf("old generation wrote: %v", err)
	}
	if _, err := m.MaterializeNextApplicationStandardTarget(ctx, current); err != nil {
		t.Fatal(err)
	}
}
