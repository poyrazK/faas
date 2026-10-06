package runtimeupgrade

// adr: 605

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeUpgradeWorkerStore struct {
	state.RuntimeUpgradeOperationStore
	cancel   context.CancelFunc
	claims   int
	advanced int
}

func (s *runtimeUpgradeWorkerStore) ClaimRuntimeUpgradeOperation(context.Context) (state.RuntimeUpgradeOperationClaim, error) {
	s.claims++
	if s.claims == 1 {
		return state.RuntimeUpgradeOperationClaim{}, errors.New("synthetic confidential database value")
	}
	s.cancel()
	return state.RuntimeUpgradeOperationClaim{}, state.ErrNotFound
}

func (s *runtimeUpgradeWorkerStore) AdvanceRuntimeUpgradeOperation(context.Context, state.RuntimeUpgradeOperationClaim) (state.RuntimeUpgradeOperation, error) {
	s.advanced++
	return state.RuntimeUpgradeOperation{}, nil
}

func TestRuntimeUpgradeWorkerRecoversWithoutLoggingDatabaseValues(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	t.Setenv("WATCHDOG_USEC", "")
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var output bytes.Buffer
		store := &runtimeUpgradeWorkerStore{cancel: cancel}
		log := slog.New(slog.NewJSONHandler(&output, nil))
		if err := RunWorker(ctx, log, store); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if store.claims != 2 || store.advanced != 0 || !strings.Contains(output.String(), "iteration failed") || strings.Contains(output.String(), "confidential") {
			t.Fatal("worker failed to recover or leaked database diagnostics", store.claims, store.advanced, output.String())
		}
	})
}
