package state_test

// adr: 602

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type acceptanceFixtureReleases struct {
	state.RuntimeReleaseStore
	guestSHA string
}

func (s acceptanceFixtureReleases) PublishRuntimeRelease(ctx context.Context, r state.RuntimeRelease) (state.RuntimeRelease, error) {
	r.GuestInitSHA256 = s.guestSHA
	r.ID = r.Identity()
	return s.RuntimeReleaseStore.PublishRuntimeRelease(ctx, r)
}

// All boot/native qualification observations here are synthetic storage tests.
func runtimeUpgradeAcceptanceFixture(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) (state.App, state.Deployment, state.Deployment, state.RuntimeInstancePublication) {
	t.Helper()
	uniqueSHA := strings.ReplaceAll(uuid.NewString(), "-", "") + strings.ReplaceAll(uuid.NewString(), "-", "")
	app, serving, candidate := runtimeUpgradeBaselineFixture(t, s, acceptanceFixtureReleases{releases, uniqueSHA})
	if _, err := s.(state.RuntimeUpgradeBaselineStore).CaptureDeploymentRuntimeUpgradeBaseline(t.Context(), candidate.ID, serving.ID); err != nil {
		t.Fatal(err)
	}
	target, err := s.(state.RuntimeUpgradeTargetStore).DeploymentRuntimeUpgradeTarget(t.Context(), candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.(state.RuntimeReleaseQualificationStore).RecordRuntimeReleaseQualification(t.Context(), runtimeQualificationFixture(target)); err != nil {
		t.Fatal(err)
	}
	key := "apps/" + app.Slug + "/candidate.ext4"
	if err := s.SetDeploymentRootfs(t.Context(), candidate.ID, "/candidate", key, 20); err != nil {
		t.Fatal(err)
	}
	if err := releases.BindDeploymentRuntimeRelease(t.Context(), candidate.ID, key, target.ID); err != nil {
		t.Fatal(err)
	}
	candidate, err = s.DeploymentByID(t.Context(), candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.CreateComputeNode(t.Context(), state.ComputeNode{Name: "upgrade-" + uuid.NewString(), TargetURL: "unix:///tmp/upgrade.sock", VPCPUs: 8,
		MemMB: 4096, MaxConcurrency: 16, AdmissionCeilingMB: 4096, VCPUBudget: 8, Lifecycle: state.NodeLifecycleActive, LastHeartbeatAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := s.CreateInstance(t.Context(), app.ID, candidate.ID, string(state.StateColdBooting), 128, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	values, err := s.(state.RuntimeAppValuesStore).RuntimeAppValuesForDeployment(t.Context(), app.AccountID, app.ID, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := state.NewRuntimeAppConfigFence(values)
	if err != nil {
		t.Fatal(err)
	}
	p := state.RuntimeInstancePublication{AccountID: app.AccountID, AppID: app.ID, InstanceID: instance.ID, NodeID: node.ID, WakeID: instance.WakeID,
		ExpectedState: string(state.StateColdBooting), Netns: "fc-" + instance.ID, HostIP: "10.100.0.8", GuestUID: 20008, Fence: fence.SecretFence, ConfigFence: fence,
		RuntimeUpgradeColdBoot: &state.RuntimeUpgradeColdBoot{TargetReleaseID: target.ID, BaseKey: target.BaseKey(), LayerKey: key, StartedAt: time.Now().UTC()}}
	return app, serving, candidate, p
}

func TestRuntimeUpgradeAcceptanceAtomicAndAttemptBound(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		_, serving, candidate, p := runtimeUpgradeAcceptanceFixture(t, s, releases)
		reader := s.(state.RuntimeUpgradeAcceptanceStore)
		if err := reader.ValidateDeploymentRuntimeUpgradeAcceptance(t.Context(), candidate.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("invented candidate proof", err)
		}
		if _, err := s.PublishOwnedInstanceRuntime(t.Context(), p); err != nil {
			t.Fatal(err)
		}
		a, err := reader.DeploymentRuntimeUpgradeAcceptance(t.Context(), candidate.ID)
		if err != nil || a.InstanceID != p.InstanceID || a.NodeID != p.NodeID || a.WakeID != p.WakeID ||
			a.RootfsKey != candidate.RootfsKey || a.TargetReleaseID != p.RuntimeUpgradeColdBoot.TargetReleaseID ||
			a.ConfigurationFingerprint != p.ConfigFence.Fingerprint || a.SecretFingerprint != p.Fence.Fingerprint || a.ReadyAt.Before(a.StartedAt) {
			t.Fatal(a, err)
		}
		if err := reader.ValidateDeploymentRuntimeUpgradeAcceptance(t.Context(), candidate.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.PublishOwnedInstanceRuntime(t.Context(), p); !errors.Is(err, state.ErrConflict) {
			t.Fatal("replayed readiness publication", err)
		}
		current, err := s.DeploymentByID(t.Context(), serving.ID)
		if err != nil || current.TrafficPercent != 100 || current.Status != state.DeployLive {
			t.Fatal("acceptance moved traffic", current, err)
		}
		if err := s.UpdateDeploymentStatus(t.Context(), candidate.ID, state.DeployFailed, "capture failed"); err != nil {
			t.Fatal(err)
		}
		retry, err := s.RetryDeploymentFromStage(t.Context(), candidate.ID, state.StageSourceDownload)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reader.DeploymentRuntimeUpgradeAcceptance(t.Context(), retry.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("retry borrowed old cold boot", err)
		}
		if _, err := s.(state.RuntimeUpgradeBaselineStore).DeploymentRuntimeUpgradeBaseline(t.Context(), retry.ID); err != nil {
			t.Fatal("retry lost review baseline", err)
		}
		unchanged, err := reader.DeploymentRuntimeUpgradeAcceptance(t.Context(), candidate.ID)
		if err != nil || unchanged != a {
			t.Fatal("retry rewrote original receipt", unchanged, err)
		}
	})
}

func TestRuntimeUpgradeAcceptanceRejectsUnprovenPublication(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		for _, tc := range []struct {
			name string
			edit func(*state.RuntimeInstancePublication)
		}{
			{"wrong base", func(p *state.RuntimeInstancePublication) {
				p.RuntimeUpgradeColdBoot.BaseKey = "base/runner-node22.ext4"
			}},
			{"wrong layer", func(p *state.RuntimeInstancePublication) { p.RuntimeUpgradeColdBoot.LayerKey = "other.ext4" }},
			{"wrong target", func(p *state.RuntimeInstancePublication) {
				p.RuntimeUpgradeColdBoot.TargetReleaseID = strings.Repeat("1", 64)
			}},
			{"wrong wake", func(p *state.RuntimeInstancePublication) { p.WakeID = uuid.NewString() }},
			{"wrong node", func(p *state.RuntimeInstancePublication) { p.NodeID = uuid.NewString() }},
			{"restored instance", func(p *state.RuntimeInstancePublication) { p.ExpectedState = string(state.StateWaking) }},
			{"paused result", func(p *state.RuntimeInstancePublication) {
				p.ExpectedState, p.TargetState = string(state.StateWaking), string(state.StateWarm)
			}},
			{"future boot", func(p *state.RuntimeInstancePublication) {
				p.RuntimeUpgradeColdBoot.StartedAt = time.Now().Add(time.Hour)
			}},
			{"expired boot", func(p *state.RuntimeInstancePublication) {
				p.RuntimeUpgradeColdBoot.StartedAt = time.Now().Add(-api.RuntimeUpgradeAcceptanceMaxAge - time.Second)
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, _, candidate, p := runtimeUpgradeAcceptanceFixture(t, s, releases)
				original := p
				tc.edit(&p)
				if _, err := s.PublishOwnedInstanceRuntime(t.Context(), p); !errors.Is(err, state.ErrConflict) {
					t.Fatal("accepted unproven readiness", err)
				}
				assertRuntimePublicationUnchanged(t.Context(), t, s, original)
				if _, err := s.(state.RuntimeUpgradeAcceptanceStore).DeploymentRuntimeUpgradeAcceptance(t.Context(), candidate.ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("rejected publication left receipt", err)
				}
			})
		}
	})
}

func TestRuntimeUpgradeAcceptanceRejectsDriftAndRevocation(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		for _, tc := range []struct {
			name string
			edit func(*testing.T, state.Store, state.App, state.Deployment, state.Deployment, state.RuntimeInstancePublication)
		}{
			{"configuration", func(t *testing.T, s state.Store, a state.App, _, _ state.Deployment, _ state.RuntimeInstancePublication) {
				if err := s.UpsertAppEnv(t.Context(), a.AccountID, a.ID, "REGION", "region-two"); err != nil {
					t.Fatal(err)
				}
			}},
			{"secret version", func(t *testing.T, s state.Store, a state.App, _, _ state.Deployment, _ state.RuntimeInstancePublication) {
				if err := s.UpsertAppSecret(t.Context(), a.AccountID, a.ID, "TOKEN", []byte("same-sealed-value")); err != nil {
					t.Fatal(err)
				}
			}},
			{"serving artifact", func(t *testing.T, s state.Store, _ state.App, serving, _ state.Deployment, _ state.RuntimeInstancePublication) {
				if err := s.SetDeploymentRootfs(t.Context(), serving.ID, "/changed", "changed.ext4", 20); err != nil {
					t.Fatal(err)
				}
			}},
			{"candidate artifact", func(t *testing.T, s state.Store, _ state.App, _, candidate state.Deployment, _ state.RuntimeInstancePublication) {
				if err := s.SetDeploymentRootfs(t.Context(), candidate.ID, "/changed", "changed.ext4", 20); err != nil {
					t.Fatal(err)
				}
			}},
			{"revocation", func(t *testing.T, s state.Store, _ state.App, _, _ state.Deployment, p state.RuntimeInstancePublication) {
				if err := s.(state.RuntimeReleaseQualificationStore).RevokeRuntimeReleaseQualification(t.Context(), p.RuntimeUpgradeColdBoot.TargetReleaseID, strings.Repeat("d", 64), strings.Repeat("1", 64)); err != nil {
					t.Fatal(err)
				}
			}},
			{"failed deployment", func(t *testing.T, s state.Store, _ state.App, _, candidate state.Deployment, _ state.RuntimeInstancePublication) {
				if err := s.UpdateDeploymentStatus(t.Context(), candidate.ID, state.DeployFailed, "capture failed"); err != nil {
					t.Fatal(err)
				}
			}},
		} {
			for _, before := range []bool{true, false} {
				name := tc.name + "/after-ready"
				if before {
					name = tc.name + "/during-boot"
				}
				t.Run(name, func(t *testing.T) {
					a, serving, candidate, p := runtimeUpgradeAcceptanceFixture(t, s, releases)
					if !before {
						if _, err := s.PublishOwnedInstanceRuntime(t.Context(), p); err != nil {
							t.Fatal(err)
						}
					}
					tc.edit(t, s, a, serving, candidate, p)
					if before {
						if _, err := s.PublishOwnedInstanceRuntime(t.Context(), p); !errors.Is(err, state.ErrConflict) {
							t.Fatal("drift crossed readiness publication", err)
						}
						assertRuntimePublicationUnchanged(t.Context(), t, s, p)
					} else if err := s.(state.RuntimeUpgradeAcceptanceStore).ValidateDeploymentRuntimeUpgradeAcceptance(t.Context(), candidate.ID); !errors.Is(err, state.ErrConflict) {
						t.Fatal("stale acceptance remained valid", err)
					}
				})
			}
		}
	})
}

func TestRuntimeUpgradeAcceptancePublicationSerializesWithRevocation(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		_, _, candidate, p := runtimeUpgradeAcceptanceFixture(t, s, releases)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var publishErr, revokeErr error
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, publishErr = s.PublishOwnedInstanceRuntime(t.Context(), p) }()
		go func() {
			defer wg.Done()
			<-start
			revokeErr = s.(state.RuntimeReleaseQualificationStore).RevokeRuntimeReleaseQualification(t.Context(), p.RuntimeUpgradeColdBoot.TargetReleaseID, strings.Repeat("d", 64), strings.Repeat("1", 64))
		}()
		close(start)
		wg.Wait()
		if revokeErr != nil || (publishErr != nil && !errors.Is(publishErr, state.ErrConflict)) {
			t.Fatal(publishErr, revokeErr)
		}
		_, receiptErr := s.(state.RuntimeUpgradeAcceptanceStore).DeploymentRuntimeUpgradeAcceptance(t.Context(), candidate.ID)
		if publishErr == nil && receiptErr != nil || publishErr != nil && !errors.Is(receiptErr, state.ErrNotFound) {
			t.Fatal("partial receipt publication", publishErr, receiptErr)
		}
		if publishErr != nil {
			assertRuntimePublicationUnchanged(t.Context(), t, s, p)
		}
		if err := s.(state.RuntimeUpgradeAcceptanceStore).ValidateDeploymentRuntimeUpgradeAcceptance(t.Context(), candidate.ID); !errors.Is(err, state.ErrConflict) && !errors.Is(err, state.ErrNotFound) {
			t.Fatal("revoked readiness usable", err)
		}
	})
}

func TestPgRuntimeUpgradeAcceptanceImmutableAndParentCleanup(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	_, _, candidate, p := runtimeUpgradeAcceptanceFixture(t, s, s)
	if _, err := s.PublishOwnedInstanceRuntime(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"UPDATE deployment_runtime_upgrade_acceptances SET ready_at=ready_at+interval '1 second' WHERE deployment_id=$1",
		"DELETE FROM deployment_runtime_upgrade_acceptances WHERE deployment_id=$1",
	} {
		if _, err := pool.Exec(t.Context(), query, candidate.ID); err == nil {
			t.Fatal("mutable acceptance", query)
		}
	}
	if _, err := pool.Exec(t.Context(), "UPDATE deployments SET deleted_at=clock_timestamp() WHERE id=$1", candidate.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateDeploymentRuntimeUpgradeAcceptance(t.Context(), candidate.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("deleted candidate retained usable acceptance", err)
	}
	if err := s.DeleteInstance(t.Context(), p.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeploymentRuntimeUpgradeAcceptance(t.Context(), candidate.ID); err != nil {
		t.Fatal("VM cleanup erased historical receipt", err)
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM deployments WHERE id=$1", candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeploymentRuntimeUpgradeAcceptance(t.Context(), candidate.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("orphan receipt", err)
	}
}
