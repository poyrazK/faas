package pgintegration_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

// Actual control-plane stores and qualified/served fixtures; no native VM claim.
func TestGitOpsRecoveryRetentionProtectsServedIdentityAndCatalog(t *testing.T) {
	for _, handoff := range []bool{false, true} {
		t.Run(fmt.Sprint("handoff-", handoff), func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				store := basic.(interface {
					intentTestStore
					requestQualificationReceiptStore
					state.EnvironmentGitOpsGraphActivationStore
					state.EnvironmentGitOpsWorkloadServingStore
					state.EnvironmentGitOpsRecoveryStore
				})
				var initialPolicy *api.EnvironmentRecoveryRetention
				if handoff {
					initialPolicy = &api.EnvironmentRecoveryRetention{Releases: 3, Seconds: 3600, MaxBytes: 1 << 20}
				}
				lease, requests := preparedRequestModeQualificationFixtureWithRetention(t, store, initialPolicy)
				var definition api.EnvironmentDefinition
				if err := json.Unmarshal(lease.Revision.Definition, &definition); err != nil {
					t.Fatal(err)
				}
				desired, err := environmentsync.Compile(definition)
				if err != nil {
					t.Fatal(err)
				}
				plan := claimedIntentPlan(t, store, lease, desired)
				for _, request := range requests {
					completeRequestQualificationReceipts(t, store, request)
				}
				release, activated, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan)
				if err != nil || !activated {
					t.Fatal("activate", err)
				}
				registerServingGateway(t, store, "recovery-edge")
				receipt, _, err := store.PrepareEnvironmentGitOpsWorkloadServing(t.Context(), lease, plan, []string{"recovery-edge"})
				if err != nil {
					t.Fatal(err)
				}
				var firstServing time.Time
				for _, route := range receipt.Routes {
					ack, _, err := store.AcknowledgeEnvironmentGitOpsWorkloadServing(t.Context(), lease, receipt.GraphID, route.Generation, "recovery-edge")
					if err != nil {
						t.Fatal(err)
					}
					if ack.ServedAt != nil {
						firstServing = *ack.ServedAt
					}
				}
				source, err := store.EnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, "production")
				if err != nil {
					t.Fatal(err)
				}

				if handoff {
					registerServingGateway(t, store, "second-recovery-edge")
					pending, ready, err := store.PrepareEnvironmentGitOpsWorkloadServing(t.Context(), lease, plan, []string{"recovery-edge", "second-recovery-edge"})
					if err != nil || ready || pending.ServedAt != nil {
						t.Fatal("handoff did not require fresh acknowledgements", pending, err)
					}
					during, err := store.ListEnvironmentGitReleases(t.Context(), source, api.ListEnvironmentGitReleasesRequest{TargetCommitSHA: lease.Revision.CommitSHA}, desired)
					if err != nil || len(during.Releases) != 1 || during.Releases[0].ArtifactsProtectedUntil == nil || !during.Releases[0].ArtifactsProtectedUntil.Equal(firstServing.Add(time.Hour)) {
						t.Fatal("handoff lost first-serving protection", during, err)
					}
					for _, route := range pending.Routes {
						for _, gateway := range []string{"recovery-edge", "second-recovery-edge"} {
							if _, _, err := store.AcknowledgeEnvironmentGitOpsWorkloadServing(t.Context(), lease, pending.GraphID, route.Generation, gateway); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
				definition.RecoveryRetention = &api.EnvironmentRecoveryRetention{Releases: 3, Seconds: 3600, MaxBytes: 1 << 20}
				desired, err = environmentsync.Compile(definition)
				if err != nil {
					t.Fatal(err)
				}
				source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("c", 40)))
				if err != nil {
					t.Fatal("approve retained history", err)
				}
				request := api.ListEnvironmentGitReleasesRequest{TargetCommitSHA: strings.Repeat("c", 40)}
				catalog, err := store.ListEnvironmentGitReleases(t.Context(), source, request, desired)
				if err != nil || len(catalog.Releases) != 1 {
					t.Fatal("catalog", catalog, err)
				}
				row := catalog.Releases[0]
				if row.ReleaseSetID != release.ID || row.ProtectionStatus != "protected" || row.ArtifactsProtectedUntil == nil || row.RetainedBytes != 8192 || catalog.RetentionBytes != 8192 || catalog.RecoveryRetention == nil {
					t.Fatalf("retention projection: %+v %+v", row, catalog)
				}
				if !row.ArtifactsProtectedUntil.Equal(firstServing.Add(time.Hour)) || row.ServedAt == nil || !row.ServedAt.Equal(firstServing) {
					t.Fatal("handoff renewed history", row)
				}
				again, err := store.ListEnvironmentGitReleases(t.Context(), source, request, desired)
				if err != nil || !again.Releases[0].ArtifactsProtectedUntil.Equal(*row.ArtifactsProtectedUntil) {
					t.Fatal("observation renewed deadline", err)
				}
				if row.ArtifactsProtectedUntil.After(time.Now().Add(3601 * time.Second)) {
					t.Fatal("deadline not anchored to serving")
				}
				for _, member := range release.Members {
					if err := store.SetDeploymentRootfs(t.Context(), member.DeploymentID, "/changed.ext4", "changed", 8192); err == nil {
						t.Fatal("retained code identity mutated")
					}
				}
				// Explicit policy removal releases only this policy's protection.
				definition.RecoveryRetention = nil
				desired, err = environmentsync.Compile(definition)
				if err != nil {
					t.Fatal(err)
				}
				source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("d", 40)))
				if err != nil {
					t.Fatal(err)
				}
				catalog, err = store.ListEnvironmentGitReleases(t.Context(), source, api.ListEnvironmentGitReleasesRequest{TargetCommitSHA: strings.Repeat("d", 40)}, desired)
				if err != nil || catalog.RetentionBytes != 0 || catalog.RecoveryRetention != nil || catalog.Releases[0].ProtectionStatus != "disabled" {
					t.Fatal("policy removal leaked retention", catalog, err)
				}
			})
		})
	}
}

func TestGitOpsRecoveryRetentionSerializesAccountReservations(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		source, desired := seed(t, basic)
		desired.Definition.RecoveryRetention = &api.EnvironmentRecoveryRetention{Releases: 3, Seconds: 3600, MaxBytes: api.EnvironmentRecoveryMaxBytes}
		desired, err := environmentsync.Compile(desired.Definition)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = basic.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("c", 40))); err != nil {
			t.Fatal(err)
		}
		inputs := []state.ApproveEnvironmentRevision{}
		for _, slug := range []string{"stage-one", "stage-two"} {
			if _, err := basic.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: slug}); err != nil {
				t.Fatal(err)
			}
			spec := source.Spec
			spec.ManifestPath = "environments/" + slug + ".yaml"
			s, err := basic.CreateEnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, slug, spec)
			if err != nil {
				t.Fatal(err)
			}
			d, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: slug, Workloads: map[string]api.EnvironmentWorkload{}, RecoveryRetention: desired.Definition.RecoveryRetention})
			if err != nil {
				t.Fatal(err)
			}
			inputs = append(inputs, approval(s, d, strings.Repeat("d", 40)))
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, input := range inputs {
			go func() {
				<-start
				_, _, err := basic.ApproveEnvironmentDesiredRevision(t.Context(), input)
				results <- err
			}()
		}
		close(start)
		success, quota := 0, 0
		for range inputs {
			err := <-results
			if err == nil {
				success++
			} else if errors.Is(err, state.ErrQuotaExceeded) {
				quota++
			} else {
				t.Fatal("unexpected concurrent approval failure", err)
			}
		}
		if success != 1 || quota != 1 {
			t.Fatalf("account budget raced: success=%d quota=%d", success, quota)
		}
	})
}
