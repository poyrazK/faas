package state_test

// adr: 608
// adr: 609

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runtimeupgrade"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRuntimeUpgradeVerificationJournalFreezesReviewAndPersistsProgress(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s state.Store = state.NewMemStore()
			if backend == "postgres" {
				s, _ = pgStore(t)
			}
			app, serving, candidate, r := completeVerificationFixture(t, s)
			store := s.(state.RuntimeUpgradeVerificationJournalStore)
			controls := runtimeupgrade.Controls{Store: s.(state.RuntimeUpgradeReservationStore)}
			sessions := []string{uuid.NewString(), uuid.NewString()}
			seedReviewedRuntimeUpgradeGateways(t, s, sessions)
			if _, err := controls.StartVerification(t.Context(), uuid.NewString(), r.ID, sessions); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("cross-account enrollment", err)
			}
			for _, bad := range [][]string{nil, {sessions[0], strings.ToUpper(sessions[0])}} {
				if _, err := controls.StartVerification(t.Context(), r.AccountID, r.ID, bad); !errors.Is(err, state.ErrInvalidArgument) {
					t.Fatal("invalid reviewed set", err)
				}
			}
			j, err := controls.StartVerification(t.Context(), r.AccountID, r.ID, sessions)
			if err != nil || j.Phase != state.RuntimeUpgradeVerificationPending || !j.DeadlineAt.Equal(j.CutoverAt.Add(api.RuntimeUpgradeVerificationMaxAge)) {
				t.Fatal(j, err)
			}
			replay, err := controls.StartVerification(t.Context(), r.AccountID, r.ID, []string{strings.ToUpper(sessions[1]), sessions[0]})
			if err != nil || !reflect.DeepEqual(j, replay) {
				t.Fatal("review replay reset journal", replay, err)
			}
			j.GatewaySessions[0] = uuid.NewString()
			retained, err := controls.VerificationStatus(t.Context(), r.AccountID, r.ID)
			if err != nil || !slices.Contains(retained.GatewaySessions, sessions[0]) || !slices.Contains(retained.GatewaySessions, sessions[1]) {
				t.Fatal("caller mutated stored review", retained, err)
			}
			for _, altered := range [][]string{{sessions[0]}, {sessions[0], uuid.NewString()}} {
				if _, err := controls.StartVerification(t.Context(), r.AccountID, r.ID, altered); !errors.Is(err, state.ErrConflict) {
					t.Fatal("review replaced", err)
				}
				if _, err := controls.Verify(t.Context(), r.AccountID, r.ID, altered); !errors.Is(err, state.ErrConflict) {
					t.Fatal("fresh observation weakened frozen set", err)
				}
			}
			claim, err := store.ClaimRuntimeUpgradeVerification(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.ClaimRuntimeUpgradeVerification(t.Context()); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("double claimed leased work", err)
			}
			if _, err := controls.VerificationStatus(t.Context(), uuid.NewString(), r.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("cross-account progress", err)
			}
			if _, err := store.AdvanceRuntimeUpgradeVerification(t.Context(), state.RuntimeUpgradeOperationClaim{ID: r.ID, LeaseToken: uuid.NewString()}); !errors.Is(err, state.ErrConflict) {
				t.Fatal("wrong lease accepted", err)
			}
			if err := s.(state.RuntimeUpgradeGatewayStore).RecordRuntimeUpgradeGateway(t.Context(), app.ID, sessions[0], candidate.ID); err != nil {
				t.Fatal(err)
			}
			pending, err := store.AdvanceRuntimeUpgradeVerification(t.Context(), claim)
			if err != nil || pending.Phase != state.RuntimeUpgradeVerificationPending || pending.Reason != "gateway_confirmation_pending" || pending.LastObservation == nil || pending.LastObservation.ConfirmedGateways != 1 {
				t.Fatal(pending, err)
			}
			persisted, err := controls.VerificationStatus(t.Context(), r.AccountID, r.ID)
			if err != nil || persisted.LeaseToken != "" || !persisted.LeaseUntil.IsZero() || persisted.LastObservation.ConfirmedGateways != 1 || !persisted.DeadlineAt.Equal(retained.DeadlineAt) {
				t.Fatal(persisted, err)
			}
			persisted.LastObservation.GatewaySessions[0] = uuid.NewString()
			persisted.LastObservation.Reason = "caller edit"
			persisted, err = controls.VerificationStatus(t.Context(), r.AccountID, r.ID)
			if err != nil || persisted.LastObservation.Reason != pending.LastObservation.Reason {
				t.Fatal("caller mutated observation", persisted, err)
			}
			if _, err := store.AdvanceRuntimeUpgradeVerification(t.Context(), claim); !errors.Is(err, state.ErrConflict) {
				t.Fatal("stale checkpoint replayed", err)
			}
			if _, err := s.UpdateDeploymentTraffic(t.Context(), serving.ID, 100, candidate.ID); err != nil {
				t.Fatal(err)
			}
			historical, err := controls.Status(t.Context(), r.AccountID, r.ID)
			if err != nil || historical.Phase != state.RuntimeUpgradeComplete {
				t.Fatal("verification altered activation history", historical, err)
			}
		})
	}
}

func TestRuntimeUpgradeVerificationJournalRejectsUnactivatedOperation(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s state.Store = state.NewMemStore()
			if backend == "postgres" {
				s, _ = pgStore(t)
			}
			_, _, _, r := runtimeUpgradeOperationFixture(t, s, s.(state.RuntimeReleaseStore))
			if _, err := s.(state.RuntimeUpgradeOperationStore).RegisterRuntimeUpgradeOperation(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			if _, err := s.(state.RuntimeUpgradeVerificationJournalStore).StartRuntimeUpgradeVerification(t.Context(), r.AccountID, r.ID, []string{uuid.NewString()}); !errors.Is(err, state.ErrConflict) {
				t.Fatal("unactivated operation enrolled", err)
			}
		})
	}
}
