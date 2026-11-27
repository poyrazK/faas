package main_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPostgresExportDownloadsAndVerifiesArchive(t *testing.T) {
	const id = "78dac86f-2d5c-4868-9249-895d9c09dc20"
	const dump = "PGDMP portable backup"
	hash := sha256.Sum256([]byte(dump))
	manifest := api.ManagedPostgresExportManifest{PostgresMajor: 16, CapturedAt: time.Now(), SizeBytes: int64(len(dump)), SHA256: hex.EncodeToString(hash[:])}
	for _, valid := range []bool{true, false} {
		t.Run(fmt.Sprintf("valid-%t", valid), func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "orders.dump")
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				route := "/v1/postgres/databases/" + restoreSwitchDB + "/exports"
				switch {
				case r.URL.Path == "/v1/capabilities":
					_, _ = w.Write([]byte(`{"registry_version":1,"plan":"pro","capabilities":[],"hidden_capabilities":[]}`))
				case r.URL.Path == "/v1/postgres/databases" && r.Method == http.MethodGet:
					_ = json.NewEncoder(w).Encode(api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: restoreSwitchDB, Name: "orders"}}})
				case r.URL.Path == route && r.Method == http.MethodPost:
					var req api.ManagedPostgresExportRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RequestID != id {
						t.Errorf("request = %+v, %v", req, err)
					}
					_ = json.NewEncoder(w).Encode(api.ManagedPostgresExport{ID: id, State: "ready", ExpiresAt: time.Now().Add(time.Hour), Manifest: &manifest})
				case r.URL.Path == route+"/"+id+"/download":
					w.Header().Set("Content-Type", "application/octet-stream")
					w.Header().Set("Content-Length", fmt.Sprint(len(dump)))
					body := dump
					if !valid {
						body = strings.Repeat("x", len(dump))
					}
					_, _ = w.Write([]byte(body))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			})
			code, out, stderr := runSwitchCLI(t, handler, "postgres", "export", "orders", "--output", output, "--request-id", id)
			if !valid {
				if code == 0 || !strings.Contains(stderr, "checksum does not match") {
					t.Fatalf("expected checksum failure, exit=%d, stdout=%q, stderr=%q", code, out, stderr)
				}
				for _, path := range []string{output, output + ".manifest.json"} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("bad archive left %s: %v", path, err)
					}
				}
				return
			}
			if code != 0 {
				t.Fatalf("exit=%d, stdout=%q, stderr=%q", code, out, stderr)
			}
			raw, err := os.ReadFile(output)
			if err != nil || string(raw) != dump {
				t.Fatalf("saved dump = %q, %v", raw, err)
			}
			raw, err = os.ReadFile(output + ".manifest.json")
			if err != nil || !strings.Contains(string(raw), manifest.SHA256) {
				t.Fatalf("saved manifest = %q, %v", raw, err)
			}
		})
	}
}
