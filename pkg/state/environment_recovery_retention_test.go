package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRecoveryRetentionSelectionUsesCountAgeAndConservativeBytes(t *testing.T) {
	now := time.Now().UTC()
	policy := &api.EnvironmentRecoveryRetention{Releases: 2, Seconds: 60, MaxBytes: 150}
	rows := []recoveryProtection{
		// Replaced 100 s ago: its age window has elapsed.
		{releaseID: "ancient", served: now.Add(-200 * time.Second), bytes: 10},
		// Served long before the age window but replaced 3 s ago.
		{releaseID: "old", served: now.Add(-100 * time.Second), bytes: 10},
		{releaseID: "third", served: now.Add(-3 * time.Second), bytes: 10},
		{releaseID: "second", served: now.Add(-2 * time.Second), bytes: 100},
		{releaseID: "first", served: now.Add(-time.Second), bytes: 100},
	}
	got := selectRecoveryProtection(rows, policy, now)
	for _, row := range got {
		want := map[string]string{"first": "protected", "second": "budget_exceeded", "third": "outside_policy", "old": "outside_policy", "ancient": "expired"}[row.releaseID]
		if row.status != want {
			t.Fatalf("%s: %s != %s", row.releaseID, row.status, want)
		}
		if row.releaseID == "first" && !row.until.IsZero() || row.releaseID == "second" && !row.until.Equal(now.Add(59*time.Second)) {
			t.Fatalf("%s deadline %s is not anchored to its replacement", row.releaseID, row.until)
		}
	}
	// Advancing observation does not renew a replaced release's deadline; the
	// current release stays protected until a newer one completes serving.
	got = selectRecoveryProtection(rows, policy, now.Add(time.Minute))
	for _, row := range got {
		if want := map[bool]string{true: "protected", false: "expired"}[row.releaseID == "first"]; row.status != want {
			t.Fatal("read renewed retention", row)
		}
	}
}

func recoveryRetentionMemoryFixture(t *testing.T) (*MemStore, *environmentGitOpsMemory, []string) {
	t.Helper()
	m := NewMemStore()
	source := EnvironmentGitSource{ID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString(), EnvironmentID: uuid.NewString(), EnvironmentSlug: "production", ApprovedRevisionID: uuid.NewString()}
	memory := &environmentGitOpsMemory{source: source, graphs: map[string]EnvironmentWorkloadGraph{}, revisions: map[string]EnvironmentDesiredRevision{}}
	definition := api.EnvironmentDefinition{RecoveryRetention: &api.EnvironmentRecoveryRetention{Releases: 2, Seconds: 3600, MaxBytes: 1000}}
	raw, _ := json.Marshal(definition)
	memory.revisions[source.ApprovedRevisionID] = EnvironmentDesiredRevision{Definition: raw}
	m.environmentGitOps = map[string]*environmentGitOpsMemory{source.ID: memory}
	m.environmentWorkloadServingReceipts = map[string]EnvironmentWorkloadServingReceipt{}
	m.projectReleaseSets = map[string]ProjectReleaseSet{}
	app := App{ID: uuid.NewString(), AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "recovery", Status: AppActive}
	m.apps[app.ID] = app
	ids := []string{}
	for i := range 3 {
		id := uuid.NewString()
		ids = append(ids, id)
		m.deployments[id] = Deployment{ID: id, AppID: app.ID, Scope: "production", Status: DeployFailed, RootfsPath: "/root.ext4", RootfsKey: "recovery/" + id, RootfsBytes: 100}
		graph := EnvironmentWorkloadGraph{ID: uuid.NewString(), SourceID: source.ID, EnvironmentID: source.EnvironmentID}
		memory.graphs[graph.ID] = graph
		release := ProjectReleaseSet{ID: uuid.NewString(), AccountID: source.AccountID, ProjectID: source.ProjectID, EnvironmentSlug: "production", Members: []ProjectReleaseMember{{AppID: app.ID, DeploymentID: id}}}
		m.projectReleaseSets[release.ID] = release
		served := time.Now().UTC().Add(time.Duration(i-3) * time.Second)
		m.environmentWorkloadServingReceipts[graph.ID] = EnvironmentWorkloadServingReceipt{GraphID: graph.ID, SourceID: source.ID, ReleaseSetID: release.ID, ServedAt: &served}
	}
	return m, memory, ids
}

func TestRecoveryRetentionFencesGCAndMetadataThenExpires(t *testing.T) {
	m, memory, ids := recoveryRetentionMemoryFixture(t)
	var definition api.EnvironmentDefinition
	_ = json.Unmarshal(memory.revisions[memory.source.ApprovedRevisionID].Definition, &definition)
	if err := m.validateRecoveryRetentionApprovalLocked(memory.source, definition); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids[1:] {
		key := m.deployments[id].RootfsKey
		if _, eligible, err := m.ClaimLayerArtifactDeletion(t.Context(), key); err != nil || eligible {
			t.Fatal("protected bytes collected", eligible, err)
		}
		if err := m.ClearDeployment(t.Context(), id, "test"); !errors.Is(err, ErrConflict) {
			t.Fatal("protected metadata cleared", err)
		}
		before := m.deployments[id]
		if err := m.SetDeploymentRootfs(t.Context(), id, "/changed", key, 200); !errors.Is(err, ErrInvalidStateTransition) {
			t.Fatal("protected identity changed", err)
		}
		after := m.deployments[id]
		if after.RootfsPath != before.RootfsPath || after.RootfsKey != before.RootfsKey || after.RootfsBytes != before.RootfsBytes {
			t.Fatal("rejected publication mutated protected artifact identity")
		}
	}
	if _, eligible, err := m.ClaimLayerArtifactDeletion(t.Context(), m.deployments[ids[0]].RootfsKey); err != nil || !eligible {
		t.Fatal("count bound did not release oldest", eligible, err)
	}
	// Enlarging the policy cannot resurrect a key already claimed by GC.
	definition.RecoveryRetention.Releases = 3
	if err := m.validateRecoveryRetentionApprovalLocked(memory.source, definition); !errors.Is(err, ErrLayerArtifactRetired) {
		t.Fatal("GC claim revived", err)
	}
	// Age each release so its successor completed serving two hours ago. The
	// replaced release expires; the current one stays protected while current.
	for id, receipt := range m.environmentWorkloadServingReceipts {
		old := receipt.ServedAt.Add(-2 * time.Hour)
		receipt.ServedAt = &old
		m.environmentWorkloadServingReceipts[id] = receipt
	}
	if _, eligible, err := m.ClaimLayerArtifactDeletion(t.Context(), m.deployments[ids[1]].RootfsKey); err != nil || !eligible {
		t.Fatal("expiry leaked a pin", eligible, err)
	}
	if _, eligible, err := m.ClaimLayerArtifactDeletion(t.Context(), m.deployments[ids[2]].RootfsKey); err != nil || eligible {
		t.Fatal("current release lost protection", eligible, err)
	}
}

func TestRecoveryRetentionAccountReservationAndNoReadRenewal(t *testing.T) {
	m, memory, _ := recoveryRetentionMemoryFixture(t)
	policy := api.EnvironmentDefinition{RecoveryRetention: &api.EnvironmentRecoveryRetention{Releases: 2, Seconds: 3600, MaxBytes: api.EnvironmentRecoveryMaxBytes}}
	for i := range 2 {
		source := memory.source
		source.ID = uuid.NewString()
		source.ApprovedRevisionID = fmt.Sprint(i)
		raw, _ := json.Marshal(policy)
		m.environmentGitOps[source.ID] = &environmentGitOpsMemory{source: source, revisions: map[string]EnvironmentDesiredRevision{source.ApprovedRevisionID: {Definition: raw}}}
	}
	if err := m.validateRecoveryRetentionApprovalLocked(memory.source, policy); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatal("account fan-out exceeded byte reservation", err)
	}
	before, _ := json.Marshal(m.layerArtifactRetention)
	a := m.recoveryProtectionLocked(memory, recoveryPolicy(memory), time.Now())
	b := m.recoveryProtectionLocked(memory, recoveryPolicy(memory), time.Now().Add(time.Second))
	after, _ := json.Marshal(m.layerArtifactRetention)
	if string(before) != string(after) || !a[0].until.Equal(b[0].until) {
		t.Fatal("listing renewed or mutated protection")
	}
	memory.source.Detached = true
	if m.recoveryProtectedDeploymentLocked(m.projectReleaseSets[a[0].releaseID].Members[0].DeploymentID) {
		t.Fatal("detach leaked protection")
	}
}

func TestRecoveryRetentionKeepsFirstCompletionAcrossGatewayHandoffs(t *testing.T) {
	m, memory, _ := recoveryRetentionMemoryFixture(t)
	var original EnvironmentWorkloadServingReceipt
	for _, receipt := range m.environmentWorkloadServingReceipts {
		original = receipt
		break
	}
	first := *original.ServedAt
	before := m.recoveryProtectionLocked(memory, recoveryPolicy(memory), time.Now())
	pending := original
	pending.ServedAt = nil
	pending.Serving = false
	if err := m.publishRecoveryServingReceiptLocked(pending); err != nil {
		t.Fatal(err)
	}
	stored := m.environmentWorkloadServingReceipts[pending.GraphID]
	if stored.ServedAt != nil || stored.FirstServedAt == nil || !stored.FirstServedAt.Equal(first) {
		t.Fatal("pending handoff changed history", stored)
	}
	now := time.Now().UTC()
	stored.Serving = true
	stored.ServedAt = &now
	if err := m.publishRecoveryServingReceiptLocked(stored); err != nil {
		t.Fatal(err)
	}
	after := m.recoveryProtectionLocked(memory, recoveryPolicy(memory), time.Now())
	if len(after) != len(before) {
		t.Fatal("handoff changed retained history", before, after)
	}
	for i := range after {
		if after[i].releaseID != before[i].releaseID || !after[i].served.Equal(before[i].served) || !after[i].until.Equal(before[i].until) {
			t.Fatal("handoff renewed retention", before[i], after[i])
		}
	}
}
