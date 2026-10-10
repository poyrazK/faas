// adr: 943
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestValidationCannotCommitAfterOwnershipTakeover(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("seed"), increment); err != nil {
		t.Fatal(err)
	}
	exported, err := f.manager.ExportState(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	_, err = f.manager.InvokeValidatedRestoreState(t.Context(), f.id, "worker", "takeover", 1, exported, uuid.NewString(), func(ctx context.Context, _ json.RawMessage) error {
		f.clock.Add(int64(api.MaxDurableEntityLease))
		if _, err := f.manager.Acquire(ctx, f.id, "new-owner"); err != nil {
			t.Fatal(err)
		}
		return nil
	})
	if !errors.Is(err, ErrStaleOwner) {
		t.Fatal(err)
	}
	view, err := f.manager.Read(t.Context(), f.id)
	if err != nil || view.Version != 1 {
		t.Fatal("obsolete validator committed", view, err)
	}
}

func TestValidatedRestoreRejectionReplayAndRequestBinding(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("seed"), withOutbox(outboxIntent())); err != nil {
		t.Fatal(err)
	}
	exported, err := f.manager.ExportState(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	deployment := uuid.NewString()
	called := 0
	validate := func(_ context.Context, candidate json.RawMessage) error { called++; candidate[0] = ' '; return nil }
	result, err := f.manager.InvokeValidatedRestoreState(t.Context(), f.id, "worker", "restore", 1, exported, deployment, validate)
	if err != nil || result.Version != 2 || called != 1 {
		t.Fatal(result, called, err)
	}
	current, err := f.manager.ExportState(t.Context(), f.id)
	if err != nil || string(current.Data) != string(exported.Data) {
		t.Fatal("validator mutated restored state", err)
	}
	replay, err := f.manager.InvokeValidatedRestoreState(t.Context(), f.id, "worker", "restore", 1, exported, deployment, func(context.Context, json.RawMessage) error {
		t.Fatal("validator ran during receipt replay")
		return nil
	})
	if err != nil || !replay.Replayed || replay.Version != 2 {
		t.Fatal(replay, err)
	}
	if _, err := f.manager.InvokeValidatedRestoreState(t.Context(), f.id, "worker", "restore", 1, exported, uuid.NewString(), validate); !errors.Is(err, ErrRequestConflict) {
		t.Fatal(err)
	}
	if _, err := f.manager.InvokeValidatedRestoreState(t.Context(), f.id, "worker", "reject", 2, exported, deployment, func(context.Context, json.RawMessage) error { return ErrRestoreRejected }); !errors.Is(err, ErrRestoreRejected) {
		t.Fatal(err)
	}
	view, err := f.manager.Read(t.Context(), f.id)
	if err != nil || view.Version != 2 {
		t.Fatal("rejected state committed", view, err)
	}
	if _, err := f.manager.InvokeValidatedRestoreState(t.Context(), f.id, "worker", "stale", 1, exported, deployment, func(context.Context, json.RawMessage) error { t.Fatal("stale operation called validator"); return nil }); !errors.Is(err, ErrRestoreObsolete) {
		t.Fatal(err)
	}
}

func TestRestoreValidationVerdictRejectsTransitionsAndMissingFields(t *testing.T) {
	for _, value := range []bool{false, true} {
		body, _ := json.Marshal(map[string]any{"protocol_version": 1, "valid": value})
		if valid, err := DecodeRestoreValidation(body); err != nil || valid != value {
			t.Fatal(valid, err)
		}
	}
	for _, body := range []string{`{"protocol_version":1}`, `{"protocol_version":2,"valid":true}`, `{"protocol_version":1,"valid":null}`, `{"protocol_version":1,"valid":true,"outbox":[]}`, `{"data":{},"result":true}`, `{"protocol_version":1,"valid":true} {}`} {
		if _, err := DecodeRestoreValidation([]byte(body)); !errors.Is(err, ErrInvalid) {
			t.Fatal(body, err)
		}
	}
}

func TestIsolatedRestoreBundleBindingAndReplay(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("seed"), increment); err != nil {
		t.Fatal(err)
	}
	exported, err := f.manager.ExportState(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	deployment := uuid.NewString()
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	callback := func(context.Context, json.RawMessage) error { return nil }
	result, err := f.manager.InvokeIsolatedValidatedRestoreState(t.Context(), f.id, "worker", "isolated", 1, exported, deployment, hash, callback)
	if err != nil || result.Version != 2 {
		t.Fatal(result, err)
	}
	result, err = f.manager.InvokeIsolatedValidatedRestoreState(t.Context(), f.id, "worker", "isolated", 1, exported, deployment, hash, func(context.Context, json.RawMessage) error { t.Fatal("replay ran validator"); return nil })
	if err != nil || !result.Replayed {
		t.Fatal(result, err)
	}
	_, err = f.manager.InvokeIsolatedValidatedRestoreState(t.Context(), f.id, "worker", "isolated", 1, exported, deployment, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", callback)
	if !errors.Is(err, ErrRequestConflict) {
		t.Fatal("changed bundle reused receipt", err)
	}
}
