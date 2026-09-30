//go:build !no_pg

package state_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgTrafficImportProjectionRefusal(t *testing.T) {
	store, _ := pgStore(t)
	trafficImportProjectionRefusal(t, store)
}

func TestPgTrafficImportConcurrentNewSlotAndReplacement(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	account, _, first := trafficProjectionOwner(t, store)
	second, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "concurrent-import", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	start := make(chan struct{})
	type result struct {
		appID string
		err   error
	}
	done := make(chan result, 2)
	for _, id := range []string{first.ID, second.ID} {
		go func() {
			<-start
			done <- result{id, store.UpsertAppOpenAPIDocIfUnderQuota(ctx, id, account.ID, numericExpansionImport(""), 0, "3.1.0", 1)}
		}()
	}
	close(start)
	winner, refused := "", 0
	for range 2 {
		outcome := <-done
		var quota *state.QuotaError
		if outcome.err == nil {
			winner = outcome.appID
		} else if errors.As(outcome.err, &quota) && quota.Observed == 1 {
			refused++
		} else {
			t.Fatalf("unexpected concurrent verdict: %v", outcome.err)
		}
	}
	if winner == "" || refused != 1 {
		t.Fatalf("new-slot race: winner=%q refused=%d", winner, refused)
	}
	if err := store.UpsertAppOpenAPIDocIfUnderQuota(ctx, winner, account.ID, numericExpansionImport("repaired"), 0, "3.1.0", 1); err != nil {
		t.Fatalf("winner replacement at cap: %v", err)
	}
	if count, err := store.CountOpenAPIImportsByAccount(ctx, account.ID); err != nil || count != 1 {
		t.Fatalf("quota changed after replacement: count=%d err=%v", count, err)
	}
}

func TestPgTrafficImportProjectionBoundaryAndLegacyRepairAtQuota(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, _, app := trafficProjectionOwner(t, store)
	q := sqlc.New()
	baseSize, err := q.MeasureTrafficPolicyProjection(ctx, pool, numericExpansionImport(""))
	if err != nil {
		t.Fatal(err)
	}
	padding := strings.Repeat("x", api.TrafficPolicyMaxContractBytes-int(baseSize))
	doc := numericExpansionImport(padding)
	if err := store.UpsertAppOpenAPIDocIfUnderQuota(ctx, app.ID, account.ID, doc, 0, "3.1.0", 1); err != nil {
		t.Fatalf("canonical document at bound: %v", err)
	}
	_, original, err := store.GetAppOpenAPIDoc(ctx, app.ID, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	params := sqlc.ReadPublicHostOpenAPIDocParams{AccountID: pgUUID(t, account.ID), AppID: pgUUID(t, app.ID), MaxBytes: api.TrafficPolicyMaxContractBytes}
	view, err := q.ReadPublicHostOpenAPIDoc(ctx, pool, params)
	if err != nil || view.Oversized || len(view.Doc) != api.TrafficPolicyMaxContractBytes {
		t.Fatalf("runtime disagrees at bound: bytes=%d oversized=%v err=%v", len(view.Doc), view.Oversized, err)
	}
	legacy := numericExpansionImport(padding + "x")
	requireTrafficProjectionError(t, store.UpsertAppOpenAPIDocIfUnderQuota(ctx, app.ID, account.ID, legacy, 0, "3.1.0", 1), "imported_openapi_contract")
	sum := sha256.Sum256(legacy)
	if _, err := pool.Exec(ctx, `UPDATE app_openapi_docs SET doc = $1, byte_size = $2, doc_sha256 = $3 WHERE app_id = $4`, legacy, len(legacy), sum[:], app.ID); err != nil {
		t.Fatal(err)
	}
	view, err = q.ReadPublicHostOpenAPIDoc(ctx, pool, params)
	if err != nil || !view.Oversized || view.Doc != nil {
		t.Fatalf("legacy import did not refuse runtime: oversized=%v err=%v", view.Oversized, err)
	}
	if err := store.UpsertAppOpenAPIDocIfUnderQuota(ctx, app.ID, account.ID, numericExpansionImport(""), 0, "3.1.0", 1); err != nil {
		t.Fatalf("repair legacy import at full quota: %v", err)
	}
	view, err = q.ReadPublicHostOpenAPIDoc(ctx, pool, params)
	if err != nil || view.Oversized || view.Doc == nil {
		t.Fatalf("fresh runtime did not recover: oversized=%v err=%v", view.Oversized, err)
	}
	_, repaired, err := store.GetAppOpenAPIDoc(ctx, app.ID, account.ID)
	if err != nil || !repaired.CapturedAt.Equal(original.CapturedAt) {
		t.Fatalf("repair changed first-import timestamp: err=%v", err)
	}
}
