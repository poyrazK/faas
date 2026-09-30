package state_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func numericExpansionImport(padding string) []byte {
	return []byte(`{"openapi":"3.1.0","info":{"title":"expansion","version":"1"},"paths":{},"x-values":[` +
		strings.Repeat("1e300,", 1499) + `1e300],"x-pad":"` + padding + `"}`)
}

func trafficImportProjectionRefusal(t *testing.T, store state.Store) {
	t.Helper()
	account, _, app := trafficProjectionOwner(t, store)
	ctx := context.Background()
	valid := []byte(`{"openapi":"3.1.0","info":{"title":"valid","version":"1"},"paths":{}}`)
	if err := store.UpsertAppOpenAPIDocIfUnderQuota(ctx, app.ID, account.ID, valid, 0, "3.1.0", 1); err != nil {
		t.Fatal(err)
	}
	oversized := []byte(`{"openapi":"3.1.0","info":{"title":"expansion","version":"1"},"paths":{},"x-values":[` + strings.Repeat("1e300,", 1799) + `1e300]}`)
	if len(oversized) >= state.OpenAPIImportMaxDocBytes {
		t.Fatal("fixture must fit the upload body cap")
	}
	for _, quota := range []bool{false, true} {
		var err error
		if quota {
			err = store.UpsertAppOpenAPIDocIfUnderQuota(ctx, app.ID, account.ID, oversized, 0, "3.1.0", 1)
		} else {
			err = store.UpsertAppOpenAPIDoc(ctx, app.ID, account.ID, oversized, 0, "3.1.0")
		}
		requireTrafficProjectionError(t, err, "imported_openapi_contract")
		_, meta, err := store.GetAppOpenAPIDoc(ctx, app.ID, account.ID)
		if err != nil || meta.ByteSize != len(valid) {
			t.Fatalf("rejected import changed stored document: err=%v", err)
		}
	}
	if err := store.UpsertAppOpenAPIDocIfUnderQuota(ctx, app.ID, account.ID, valid, 0, "3.1.0", 1); err != nil {
		t.Fatalf("replacement at quota cap: %v", err)
	}
	if count, err := store.CountOpenAPIImportsByAccount(ctx, account.ID); err != nil || count != 1 {
		t.Fatalf("replacement consumed another slot: count=%d err=%v", count, err)
	}
	if err := store.DeleteAppOpenAPIDoc(ctx, app.ID, account.ID); err != nil {
		t.Fatalf("delete recovery: %v", err)
	}
}

func TestMemTrafficImportProjectionRefusal(t *testing.T) {
	trafficImportProjectionRefusal(t, state.NewMemStore())
}
