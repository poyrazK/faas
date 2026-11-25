package main_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPostgresImportUploadsVerifiedArchive(t *testing.T) {
	const id = "78dac86f-2d5c-4868-9249-895d9c09dc20"
	const dump = "PGDMP portable backup"
	hash := sha256.Sum256([]byte(dump))
	manifest := api.ManagedPostgresExportManifest{
		PostgresMajor: 16, CapturedAt: time.Now().UTC(), DatabaseBytes: 8192,
		SizeBytes: int64(len(dump)), SHA256: hex.EncodeToString(hash[:]),
	}
	manifest.RestoreReceipt = manifest.ArchiveManifest().Receipt("test identity")
	file := filepath.Join(t.TempDir(), "orders.dump")
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file+".manifest.json", raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(dump), 0o600); err != nil {
		t.Fatal(err)
	}
	var uploads atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/capabilities":
			_, _ = w.Write([]byte(`{"registry_version":1,"plan":"pro","capabilities":[],"hidden_capabilities":[]}`))
		case r.URL.Path == "/v1/postgres/databases" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: restoreSwitchDB, Name: "orders-recovery"}}})
		case r.URL.Path == "/v1/postgres/databases/"+restoreSwitchDB+"/imports/"+id+"/archive" && r.Method == http.MethodPost:
			uploads.Add(1)
			parts, err := r.MultipartReader()
			if err != nil {
				t.Errorf("multipart request: %v", err)
				http.Error(w, "invalid upload", http.StatusBadRequest)
				return
			}
			for _, want := range []struct {
				name    string
				content string
			}{{"manifest", string(raw)}, {"archive", dump}} {
				part, err := parts.NextPart()
				if err != nil {
					t.Errorf("missing %s part: %v", want.name, err)
					http.Error(w, "invalid upload", http.StatusBadRequest)
					return
				}
				content, err := io.ReadAll(part)
				if err != nil || part.FormName() != want.name || string(content) != want.content {
					t.Errorf("part %q: %s, %v", part.FormName(), content, err)
				}
			}
			if _, err := parts.NextPart(); err != io.EOF {
				t.Errorf("end of upload: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(api.ManagedPostgresImportStatus{State: "running", OperationID: id})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	})
	args := []string{"postgres", "import", "orders-recovery", "--from-file", file, "--request-id", strings.ToUpper(id)}
	if code, out, stderr := runSwitchCLI(t, handler, args...); code != 0 || !strings.Contains(out+stderr, id) {
		t.Fatalf("exit=%d, stdout=%q, stderr=%q", code, out, stderr)
	}
	corrupt := dump[:len(dump)-1] + "!"
	if err := os.WriteFile(file, []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, stderr := runSwitchCLI(t, handler, args...); code == 0 || !strings.Contains(stderr, "does not match its manifest") {
		t.Fatalf("expected local checksum failure, exit=%d, stdout=%q, stderr=%q", code, out, stderr)
	}
	if got := uploads.Load(); got != 1 {
		t.Fatalf("upload requests = %d, want 1", got)
	}
}
