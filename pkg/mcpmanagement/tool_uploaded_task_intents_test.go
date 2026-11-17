package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
	"github.com/onebox-faas/faas/pkg/tasktoolsource/sourcecheck"
)

func TestHostedTaskSourceCaptureMatchesReviewedWalk(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "a/empty", "z"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, mode := range map[string]os.FileMode{"!before-root": 0600, "a/x": 0600, "a.z": 0700, "z/a": 0600} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), mode); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := captureTaskSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := tasktoolsource.Digest(entries)
	if err != nil {
		t.Fatal(err)
	}
	original, err := toolSourceDigest(dir)
	if err != nil || original != digest {
		t.Fatalf("digest %s != %s: %v", digest, original, err)
	}
	for _, mutate := range []func([]api.HostedTaskSourceEntry){func(e []api.HostedTaskSourceEntry) { e[0].Path = "../outside" }, func(e []api.HostedTaskSourceEntry) { e[1].Path = ".gregale" }, func(e []api.HostedTaskSourceEntry) { e[1].Mode = 0777 }, func(e []api.HostedTaskSourceEntry) { e[0].Directory = false }} {
		bad := append([]api.HostedTaskSourceEntry(nil), entries...)
		mutate(bad)
		if _, err := tasktoolsource.Digest(bad); err == nil {
			t.Fatal("unsafe bundle accepted")
		}
	}
	if err := os.Symlink("a.z", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := captureTaskSource(dir); err == nil {
		t.Fatal("symlink retained")
	}
}

// The backend is independent of bridge instances. A stored receipt survives a
// closed MCP session and a new bridge, including a lost response after commit.
func TestHostedTaskSourcePrepareGetReconnectProtocols(t *testing.T) {
	for _, protocol := range []string{api.MCPProtocolVersion, api.MCPLegacyProtocolVersion} {
		for _, archive := range []bool{false, true} {
			t.Run(protocol+map[bool]string{true: "/archive", false: "/inline"}[archive], func(t *testing.T) {
				f := newUploadedTaskFixture(t)
				var mu sync.Mutex
				records := map[string]api.HostedTaskSourceIntent{}
				puts := 0
				lose := false
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					prefix := "/v1/task-tool-source-intents/"
					if !strings.HasPrefix(r.URL.Path, prefix) {
						f.serve(w, r)
						return
					}
					mu.Lock()
					defer mu.Unlock()
					id := strings.TrimPrefix(r.URL.Path, prefix)
					if r.Method == http.MethodGet {
						record, ok := records[id]
						if !ok {
							api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", ""))
							return
						}
						_ = json.NewEncoder(w).Encode(record)
						return
					}
					var in api.HostedTaskSourcePrepare
					if json.NewDecoder(r.Body).Decode(&in) != nil {
						t.Error("invalid body")
						w.WriteHeader(400)
						return
					}
					review, err := sourcecheck.ValidatePrepare(f.account.ID, in)
					if err != nil {
						t.Errorf("invalid retained review: %v", err)
						w.WriteHeader(400)
						return
					}
					record, err := tasktoolsource.NewRecord(f.account.ID, uuid.NewString(), id, review, time.Now())
					if err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					puts++
					records[id] = record
					if lose {
						api.WriteProblem(w, api.NewProblem(503, "lost_reply", "Lost reply", "committed"))
						return
					}
					_ = json.NewEncoder(w).Encode(record)
				}))
				defer backend.Close()
				opts := f.opts
				opts.APIBase = backend.URL
				first := true
				newBridge := func() (*mcp.ClientSession, func()) {
					if !first {
						opts.uploads = mustTaskUploadStore(t)
					}
					first = false
					cs, _ := connectSelectedTestServer(t, backend, opts, protocol)
					return cs, func() { _ = cs.Close() }
				}
				cs, closeBridge := newBridge()
				args := uploadedTaskArgs(t)
				planned := publicationCall(t, t.Context(), cs, "gregale_plan_uploaded_task_tool_deploy", args, false)
				raw, _ := json.Marshal(planned)
				var review uploadedTaskReview
				if json.Unmarshal(raw, &review) != nil {
					t.Fatal("decode review")
				}
				prepare := prepareUploadedTaskArgs{planUploadedTaskArgs: args, IntentID: strings.Repeat("d", 64), ExpectedReviewSHA256: review.Fingerprint}
				if archive {
					prepare.Web = f.archived(t, prepare.Web)
					prepare.Worker = f.archived(t, prepare.Worker)
				}
				lose = true
				publicationCall(t, t.Context(), cs, "gregale_prepare_uploaded_task_tool_deploy", prepare, true)
				closeBridge()
				cs, closeBridge = newBridge()
				defer closeBridge()
				lose = false
				recovered := publicationCall(t, t.Context(), cs, "gregale_prepare_uploaded_task_tool_deploy", prepare, false)
				got := publicationCall(t, t.Context(), cs, "gregale_get_uploaded_task_tool_deploy", getUploadedTaskIntentArgs{prepare.IntentID}, false)
				if !reflect.DeepEqual(recovered, got) {
					t.Fatalf("retry changed record: %s / %s", recovered, got)
				}
				if archive {
					prepare.Web.ExpectedSHA256 = strings.Repeat("f", 64)
				} else {
					prepare.Web.Files[2].Content = "changed bytes"
				}
				publicationCall(t, t.Context(), cs, "gregale_prepare_uploaded_task_tool_deploy", prepare, true)
				if puts != 1 || f.cli.Load() != 0 || f.writes.Load() != 0 {
					t.Fatalf("puts=%d CLI=%d unexpected writes=%d", puts, f.cli.Load(), f.writes.Load())
				}
				gotRaw, _ := json.Marshal(got)
				for _, secret := range []string{"customer source must not execute", "ciphertext", "DATABASE_URL", "tasks-release.js"} {
					if strings.Contains(string(gotRaw), secret) {
						t.Fatalf("source leaked: %s", secret)
					}
				}
			})
		}
	}
}
func mustTaskUploadStore(t *testing.T) *uploadStore {
	t.Helper()
	s, err := newUploadStore(filepath.Join(t.TempDir(), "uploads"), "https://api.example/mcp")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHostedTaskSourceNativeValidationRejectsTampering(t *testing.T) {
	f := newUploadedTaskFixture(t)
	_, m, err := newManagementServer(api.NewClient(f.backend.URL, testToken), f.opts)
	if err != nil {
		t.Fatal(err)
	}
	args := uploadedTaskArgs(t)
	_, err = m.withUploadedTaskReview(context.Background(), args, func(review uploadedTaskReview, web, worker string) (any, error) {
		w, err := captureTaskSource(web)
		if err != nil {
			return nil, err
		}
		k, err := captureTaskSource(worker)
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(review)
		in := api.HostedTaskSourcePrepare{Review: raw, Sources: map[string][]api.HostedTaskSourceEntry{"web": w, "worker": k}}
		if _, err := sourcecheck.ValidatePrepare(f.account.ID, in); err != nil {
			t.Fatal(err)
		}
		changed := review
		changed.AccountID = uuid.NewString()
		changed.Fingerprint = tasktoolsource.Fingerprint(changed)
		in.Review, _ = json.Marshal(changed)
		if _, err := sourcecheck.ValidatePrepare(f.account.ID, in); err == nil {
			t.Fatal("cross account accepted")
		}
		in.Review = raw
		in.Sources["web"][1].Content = []byte("tampered")
		if _, err := sourcecheck.ValidatePrepare(f.account.ID, in); err == nil {
			t.Fatal("changed source accepted")
		}
		record, err := tasktoolsource.NewRecord(f.account.ID, uuid.NewString(), strings.Repeat("a", 64), review, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if tasktoolsource.ValidateRecord(record, f.account.ID, record.ID) != nil {
			t.Fatal("record rejected")
		}
		record.ReadyToApply = true
		if tasktoolsource.ValidateRecord(record, f.account.ID, record.ID) == nil {
			t.Fatal("authority accepted")
		}
		if tasktoolsource.View(record, true, record.ExpiresAt).SourceState != "expired" {
			t.Fatal("expiry not enforced")
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
