//go:build linux

package e2e_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The API commits normally. Only its reply is dropped or held until the CLI
// dies, so the recovered ID must come from the durable upload protocol.
func TestE2E_Start_LostSubmissionReplyRecoversExactDeployment(t *testing.T) {
	bin := buildGregale(t)
	for _, scope := range []string{"default", "staging"} {
		for _, update := range []bool{false, true} {
			for _, failure := range []string{"lost-reply", "process-killed"} {
				t.Run(scope+"/update="+strconv.FormatBool(update)+"/"+failure, func(t *testing.T) {
					environment := scope
					if environment == "default" {
						environment = ""
					}
					f := newStartAcceptanceFixture(t, bin, "start-lost-"+scope, true, environment)
					origin, err := url.Parse(f.platform.h.APIDURL)
					if err != nil {
						t.Fatal(err)
					}
					forward := httputil.NewSingleHostReverseProxy(origin)
					var armed, reported atomic.Bool
					var recoveryWrites atomic.Int32
					accepted := make(chan string, 1)
					release := make(chan struct{})
					var released atomic.Bool
					releaseReply := func() {
						if released.CompareAndSwap(false, true) {
							close(release)
						}
					}
					dropped := errors.New("fixture dropped commit reply")
					forward.ModifyResponse = func(response *http.Response) error {
						if !armed.Load() || response.Request.Method != http.MethodPost || !strings.HasSuffix(response.Request.URL.Path, "/commit") {
							return nil
						}
						wire, err := io.ReadAll(response.Body)
						_ = response.Body.Close()
						if err != nil {
							return err
						}
						if response.StatusCode == http.StatusCreated {
							var receipt struct {
								ID string `json:"id"`
							}
							if json.Unmarshal(wire, &receipt) != nil || receipt.ID == "" {
								return errors.New("fixture commit omitted deployment ID")
							}
							if reported.CompareAndSwap(false, true) {
								accepted <- receipt.ID
							}
						}
						if failure == "process-killed" {
							select {
							case <-release:
							case <-response.Request.Context().Done():
							}
						}
						return dropped
					}
					forward.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
						if !errors.Is(err, dropped) {
							t.Errorf("unexpected API adapter failure: %v", err)
						}
						connection, _, hijackErr := w.(http.Hijacker).Hijack()
						if hijackErr != nil {
							t.Error(hijackErr)
							return
						}
						_ = connection.Close()
					}
					adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method != http.MethodGet && !strings.HasSuffix(r.URL.Path, "/response-check") {
							recoveryWrites.Add(1)
						}
						forward.ServeHTTP(w, r)
					}))
					t.Cleanup(adapter.Close)
					t.Cleanup(releaseReply)
					for i, value := range f.cliEnv {
						if strings.HasPrefix(value, "FAAS_API=") {
							f.cliEnv[i] = "FAAS_API=" + adapter.URL
						}
					}
					s := f.start(t)
					var previous string
					if update {
						f.selectSource(s)
						s.answer("Deploy this app (y/N) [n]: ", "y")
						first := f.accepted(t)
						f.publish(t, first, "v1")
						f.assertResponse(t, first, "v1", s)
						previous = first.ID
						original, err := os.ReadFile(filepath.Join(f.source, "handler.js"))
						if err != nil {
							t.Fatal(err)
						}
						writeStartAcceptanceFile(t, filepath.Join(f.source, "handler.js"), string(original)+"\n// reviewed fixture update\n")
						armed.Store(true)
						s.answer("Choose [1]: ", "2")
					} else {
						armed.Store(true)
						f.selectSource(s)
					}
					s.answer("Deploy this app (y/N) [n]: ", "y")
					var id string
					select {
					case id = <-accepted:
					case <-s.done:
						t.Fatalf("CLI exited before the commit fault: %s", s.transcript())
					case <-t.Context().Done():
						t.Fatal(t.Context().Err())
					case <-time.After(40 * time.Second):
						t.Fatalf("API did not commit the reviewed source: %s", s.transcript())
					}
					if failure == "process-killed" {
						if err := s.cmd.Process.Kill(); err != nil {
							t.Fatal(err)
						}
						s.wait(-1)
					} else {
						s.wait(3)
					}
					releaseReply()
					armed.Store(false)
					paths, err := filepath.Glob(filepath.Join(f.stateDir, "gregale", "start", "*.json"))
					if err != nil || len(paths) != 1 {
						t.Fatal("pending submission record missing")
					}
					wire, err := os.ReadFile(paths[0])
					var saved struct {
						Version      int    `json:"version"`
						DeploymentID string `json:"deployment_id"`
						VerifiedID   string `json:"verified_deployment_id"`
						Pending      *struct {
							UploadID string `json:"upload_id"`
						} `json:"pending_submission"`
					}
					if err != nil || json.Unmarshal(wire, &saved) != nil || saved.Version != 2 || saved.Pending == nil || saved.Pending.UploadID == "" || saved.DeploymentID != previous || saved.VerifiedID != previous {
						t.Fatal("lost reply did not retain its pending upload and previous verified deployment")
					}
					dep, err := f.platform.store.DeploymentByID(t.Context(), id)
					if err != nil {
						t.Fatal(err)
					}
					f.publish(t, dep, "recovered")
					if err := os.RemoveAll(filepath.Join(f.stateDir, "gregale", "uploads")); err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(filepath.Join(f.source, "handler.js")); err != nil {
						t.Fatal(err)
					}
					recoveryWrites.Store(0)
					s = f.start(t)
					s.expect("A source submission needs recovery before another launch.")
					s.answer("Choose [1]: ", "1")
					f.assertResponse(t, dep, "recovered", s)
					s.answer("Choose [1]: ", "1")
					s.wait(0)
					f.assertSession(t, id, id)
					rows, err := f.platform.store.ListDeploymentsForApp(t.Context(), f.platform.app.ID, 10, 0)
					want := 1
					if update {
						want++
					}
					if err != nil || len(rows) != want || recoveryWrites.Load() != 0 {
						t.Fatalf("pending recovery submitted another mutation: deployments=%d writes=%d err=%v", len(rows), recoveryWrites.Load(), err)
					}
				})
			}
		}
	}
}
