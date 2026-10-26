package faas_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/poyrazK/faas/sdk/go/preview"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestBackupAndPreviewSDKSelectors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/counter/entities/restore/preview" {
			var request preview.DurableEntityRestoreRequest
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&request) != nil || request.ExpectedVersion != ^uint64(0) || r.Header.Get("Idempotency-Key") != "" {
				t.Error("invalid read-only preview")
			}
			_ = json.NewEncoder(w).Encode(preview.DurableEntityRestorePreview{CurrentVersion: ^uint64(0), SourceVersion: 1, Compatibility: "unverified"})
			return
		}
		if r.Method != http.MethodGet || r.URL.Query().Get("key") != "doc/?雪" {
			t.Error(r.URL)
		}
		if r.URL.Path == "/v1/apps/counter/entities/backups" {
			if r.URL.Query().Get("cursor") != "opaque+/=" {
				t.Error("cursor changed")
			}
			_ = json.NewEncoder(w).Encode(preview.DurableEntityBackupPage{Items: []preview.DurableEntityBackupInfo{{ID: "20261009T120000Z", Version: 1}}})
			return
		}
		if r.URL.Query().Get("backup_id") != "20261009T120000Z" {
			t.Error("missing slot")
		}
		_ = json.NewEncoder(w).Encode(preview.DurableEntityBackup{Export: preview.DurableEntityStateExport{Version: 1, Data: json.RawMessage(`{}`)}})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	selectors := preview.DurableEntityInspectRequest{Namespace: "documents", Key: "doc/?雪"}
	if page, err := client.ListDurableEntityBackups(faas.SDKTestContext(t), "counter", selectors, "opaque+/="); err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	backup, err := client.GetDurableEntityBackup(faas.SDKTestContext(t), "counter", selectors, "20261009T120000Z")
	if err != nil {
		t.Fatal(err)
	}
	if preview, err := client.PreviewDurableEntityRestore(faas.SDKTestContext(t), "counter", preview.DurableEntityRestoreRequest{Namespace: selectors.Namespace, Key: selectors.Key, ExpectedVersion: ^uint64(0), Export: backup.Export}); err != nil || preview.CurrentVersion != ^uint64(0) {
		t.Fatal(preview, err)
	}
}
