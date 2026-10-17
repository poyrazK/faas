package state_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
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

func trafficImportYAMLProjectionRecovery(t *testing.T, store state.Store) {
	t.Helper()
	account, _, app := trafficProjectionOwner(t, store)
	upload := []byte("openapi: 3.1.0\ninfo: {title: yaml, version: '1'}\npaths: {/declared: {get: {responses: {'200': {description: ok}}}}}\n")
	for _, quota := range []bool{false, true} {
		write := func(document []byte) error {
			if quota {
				return store.UpsertAppOpenAPIDocIfUnderQuota(t.Context(), app.ID, account.ID, document, 1, "3.1.0", 1)
			}
			return store.UpsertAppOpenAPIDoc(t.Context(), app.ID, account.ID, document, 1, "3.1.0")
		}
		if err := write(upload); err != nil {
			t.Fatal(err)
		}
		stored, metadata, err := store.GetAppOpenAPIDoc(t.Context(), app.ID, account.ID)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]json.RawMessage
		if err := json.Unmarshal(stored, &decoded); err != nil {
			t.Fatalf("stored YAML was not normalized for JSONB: %v", err)
		}
		if !bytes.Contains(decoded["paths"], []byte("/declared")) {
			t.Fatal("normalization lost declared routes")
		}
		digest := sha256.Sum256(upload)
		if metadata.ByteSize != len(upload) || !bytes.Equal(metadata.DocSHA256, digest[:]) {
			t.Fatal("normalization changed upload metadata")
		}
		expanded := []byte("openapi: 3.1.0\ninfo: {title: aliases, version: '1'}\npaths: {}\nx-payload: &payload '" + strings.Repeat("a", 25000) + "'\nx-copies: [" + strings.Repeat("*payload,", 24) + "*payload]\n")
		if len(expanded) > state.OpenAPIImportMaxDocBytes {
			t.Fatal("YAML fixture must fit the upload cap")
		}
		requireTrafficProjectionError(t, write(expanded), "imported_openapi_contract")
		after, afterMetadata, err := store.GetAppOpenAPIDoc(t.Context(), app.ID, account.ID)
		if err != nil || !bytes.Equal(after, stored) || !bytes.Equal(afterMetadata.DocSHA256, metadata.DocSHA256) {
			t.Fatalf("rejected YAML expansion changed the saved import: %v", err)
		}
	}
}

func TestMemTrafficImportYAMLProjectionRecovery(t *testing.T) {
	trafficImportYAMLProjectionRecovery(t, state.NewMemStore())
}
