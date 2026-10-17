package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

func TestGitOpsRecoveryCatalogPaginatesOnlyCompletedCurrentSourceHistory(t *testing.T) {
	m := NewMemStore()
	m.environmentGitOps = map[string]*environmentGitOpsMemory{}
	m.projectReleaseSets = map[string]ProjectReleaseSet{}
	m.environmentWorkloadServingReceipts = map[string]EnvironmentWorkloadServingReceipt{}
	source := EnvironmentGitSource{ID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString(), EnvironmentID: uuid.NewString(), EnvironmentSlug: "production"}
	memory := &environmentGitOpsMemory{source: source, graphs: map[string]EnvironmentWorkloadGraph{}, revisions: map[string]EnvironmentDesiredRevision{}}
	m.environmentGitOps[source.ID] = memory
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "production", Workloads: map[string]api.EnvironmentWorkload{"api": {Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/api@sha256:" + strings.Repeat("a", 64)}}}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ids := []string{}
	for i := 0; i < api.EnvironmentGitRecoveryPageSize+2; i++ {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
		graph := EnvironmentWorkloadGraph{ID: uuid.NewString(), SourceID: source.ID, EnvironmentID: source.EnvironmentID}
		memory.graphs[graph.ID] = graph
		m.projectReleaseSets[id] = ProjectReleaseSet{ID: id, AccountID: source.AccountID, ProjectID: source.ProjectID, EnvironmentSlug: source.EnvironmentSlug, CreatedAt: now}
		m.environmentWorkloadServingReceipts[graph.ID] = EnvironmentWorkloadServingReceipt{GraphID: graph.ID, ReleaseSetID: id, ServedAt: &now}
		ids = append(ids, id)
	}
	// A newer generic release and incomplete receipt must not hide real history.
	unserved := uuid.NewString()
	m.projectReleaseSets[unserved] = ProjectReleaseSet{ID: unserved, AccountID: source.AccountID, ProjectID: source.ProjectID, EnvironmentSlug: source.EnvironmentSlug, CreatedAt: now.Add(time.Hour)}
	graph := EnvironmentWorkloadGraph{ID: uuid.NewString(), SourceID: source.ID, EnvironmentID: source.EnvironmentID}
	memory.graphs[graph.ID] = graph
	m.environmentWorkloadServingReceipts[graph.ID] = EnvironmentWorkloadServingReceipt{GraphID: graph.ID, ReleaseSetID: unserved}
	request := api.ListEnvironmentGitReleasesRequest{TargetCommitSHA: strings.Repeat("a", 40)}
	out, err := m.ListEnvironmentGitReleases(t.Context(), source, request, desired)
	if err != nil || len(out.Releases) != api.EnvironmentGitRecoveryPageSize || out.NextBefore != ids[2] || out.Releases[0].ReleaseSetID != ids[len(ids)-1] {
		t.Fatalf("page: %+v %v", out, err)
	}
	for _, entry := range out.Releases {
		if entry.Eligibility != "blocked" || len(entry.BlockingReasons) != 1 || entry.BlockingReasons[0] != "rollback_release_unproven" {
			t.Fatal("incomplete evidence became eligible", entry)
		}
	}
	request.Before = out.NextBefore
	second, err := m.ListEnvironmentGitReleases(t.Context(), source, request, desired)
	if err != nil || len(second.Releases) != 2 || second.NextBefore != "" || second.Releases[0].ReleaseSetID != ids[1] {
		t.Fatalf("second page %+v %v", second, err)
	}
	for _, id := range []string{unserved, uuid.NewString()} {
		request.Before = id
		if _, err := m.ListEnvironmentGitReleases(t.Context(), source, request, desired); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign/unserved cursor accepted", err)
		}
	}
	foreign := source
	foreign.AccountID = uuid.NewString()
	request.Before = ""
	if _, err := m.ListEnvironmentGitReleases(t.Context(), foreign, request, desired); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign history accepted", err)
	}
	stale := source
	stale.Generation++
	if _, err := m.ListEnvironmentGitReleases(t.Context(), stale, request, desired); !errors.Is(err, ErrConflict) {
		t.Fatal("stale source accepted", err)
	}
	before, _ := json.Marshal(m.projectReleaseSets)
	if _, err := m.ListEnvironmentGitReleases(t.Context(), source, request, desired); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(m.projectReleaseSets)
	if string(before) != string(after) {
		t.Fatal("catalog mutated retention")
	}
	// Rebinding cannot discover the original source's receipts.
	rebound := source
	rebound.ID = uuid.NewString()
	m.environmentGitOps[rebound.ID] = &environmentGitOpsMemory{source: rebound, graphs: map[string]EnvironmentWorkloadGraph{}}
	empty, err := m.ListEnvironmentGitReleases(t.Context(), rebound, request, desired)
	if err != nil || len(empty.Releases) != 0 {
		t.Fatal("rebound source borrowed history", empty, err)
	}
	request.Before = ids[0]
	if _, err := m.ListEnvironmentGitReleases(t.Context(), rebound, request, desired); !errors.Is(err, ErrNotFound) {
		t.Fatal("rebound source borrowed cursor", err)
	}
}

func TestGitOpsRecoveryCatalogReleaseDeadlineIsNotEligibility(t *testing.T) {
	now := time.Now().UTC()
	expired := now.Add(-time.Hour)
	plan := api.PreviewEnvironmentGitRollbackResponse{BlockingReasons: []string{}}
	entry, err := recoveryRelease(ProjectReleaseSet{ExpiresAt: &expired}, plan, nil, now)
	if err != nil || entry.Eligibility != "eligible" || entry.RetentionStatus != "compatibility_window_elapsed" {
		t.Fatal("compatibility expiry inferred artifact eligibility", entry, err)
	}
	if _, err := recoveryRelease(ProjectReleaseSet{}, plan, errors.New("private failure"), now); err == nil {
		t.Fatal("storage failure became eligibility")
	}
}
