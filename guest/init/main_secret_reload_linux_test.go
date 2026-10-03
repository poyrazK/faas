//go:build linux

package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkloadSecretReloadPublishesAndReportsItsOwnIdentity(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("guest reloader requires root-owned projection directories")
	}
	for _, workload := range []string{"", "worker"} {
		for _, outcome := range []string{"queued", "signal_failed", "projection_failed", "revision_failed"} {
			t.Run(workload+"/"+outcome, func(t *testing.T) {
				dir := t.TempDir()
				projection, revision := filepath.Join(dir, "projection", "secrets"), filepath.Join(dir, "projection", "revision")
				if outcome == "projection_failed" {
					if err := os.WriteFile(filepath.Dir(projection), []byte("not-a-directory"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				sup := &Supervisor{}
				if outcome == "signal_failed" {
					process, err := os.FindProcess(os.Getpid())
					if err != nil {
						t.Fatal(err)
					}
					if err := process.Release(); err != nil {
						t.Fatal(err)
					}
					sup.trackRuntimeSecretCommand(&exec.Cmd{Process: process}, runtimeSecretSnapshot{Secrets: map[string]string{"SHARED": "old"}}, "")
					sup.markStarted()
					sup.markHealthy()
				}
				requests := make(chan runtimeConfigRequest, 2)
				previous := dialRuntimeConfigHost
				t.Cleanup(func() { dialRuntimeConfigHost = previous })
				dialRuntimeConfigHost = func() (net.Conn, error) {
					client, server := net.Pipe()
					go func() {
						defer server.Close()
						body, err := readRuntimeConfigFrame(server)
						var request runtimeConfigRequest
						if err != nil || json.Unmarshal(body, &request) != nil {
							return
						}
						response := runtimeConfigResponse{Accepted: true}
						if request.Kind == "secrets" {
							fresh := map[string]string{"SHARED": "new-" + workload}
							response = runtimeConfigResponse{Secrets: &fresh, Revision: strings.Repeat("b", 64)}
						}
						encoded, _ := json.Marshal(response)
						_ = writeRuntimeConfigFrame(server, encoded)
						requests <- request
					}()
					return client, nil
				}
				secrets := newRuntimeSecretsState(map[string]string{"SHARED": "old", "REVOKED": "remove"})
				secrets.projection = &runtimeSecretProjection{secretsPath: projection, revisionPath: revision, uid: 0}
				if outcome == "revision_failed" {
					if err := secrets.publishProjection(secrets.snapshot(), strings.Repeat("a", 64)); err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(revision); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(revision, 0700); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithCancel(context.Background())
				done := startRuntimeSecretReloaderForWorkload(ctx, api.AppManifest{User: "0", SecretReloadSignal: "SIGHUP"}, secrets, sup, nil, workload)
				t.Cleanup(func() { cancel(); <-done })
				for _, kind := range []string{"secrets", "secret_reload_status"} {
					select {
					case request := <-requests:
						if request.Kind != kind || request.WorkloadName != workload {
							t.Fatalf("request crossed workload identity: %+v", request)
						}
						if kind == "secret_reload_status" {
							projectionStatus, signalStatus, errorCode := "updated", "queued", ""
							if outcome == "signal_failed" {
								signalStatus, errorCode = "failed", outcome
							} else if outcome == "projection_failed" || outcome == "revision_failed" {
								projectionStatus, signalStatus, errorCode = "failed", "not_attempted", "projection_failed"
							}
							if request.Revision != strings.Repeat("b", 64) || request.Projection != projectionStatus || request.Signal != signalStatus || request.ErrorCode != errorCode {
								t.Fatalf("reload outcome = %+v", request)
							}
						}
					case <-time.After(5 * time.Second):
						t.Fatal("reloader did not fetch and report")
					}
				}
				if outcome == "revision_failed" {
					got := readRuntimeSecretSnapshot(t, filepath.Join(filepath.Dir(projection), secretReloadSnapshotFileName))
					if got.Revision != strings.Repeat("a", 64) || got.Secrets["SHARED"] != "old" || secrets.snapshot()["SHARED"] != "old" {
						t.Fatal("failed revision publication advanced values or restart state")
					}

				}
				if outcome != "projection_failed" && outcome != "revision_failed" {
					body, err := os.ReadFile(projection)
					var values map[string]string
					if err != nil || json.Unmarshal(body, &values) != nil || len(values) != 1 || values["SHARED"] != "new-"+workload || len(secrets.snapshot()) != 1 {
						t.Fatalf("workload projection = %s %v", body, err)
					}
				}
			})
		}
	}
}

func TestMainSupervisorRestartsWithCurrentSecretProjection(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "env")
	initial := map[string]string{"DATABASE_URL": "old", "REVOKED_TOKEN": "old-token"}
	spec := workloadSpec{Name: "main", Type: "main", runtimeSecrets: newRuntimeSecretsState(initial)}
	var starts []runtimeConfigRequest
	spec.runtimeSecrets.process.transport = func(req runtimeConfigRequest) error {
		if req.Kind == "secret_generation_start" {
			starts = append(starts, req)
		}
		return nil
	}
	manifest := api.AppManifest{User: strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()), SecretReloadSignal: "SIGHUP", Entrypoint: []string{
		"/bin/sh", "-c", `printf '%s|%s|%s|%s' "$DATABASE_URL" "${REVOKED_TOKEN-unset}" "$FAAS_SECRETS_RELOAD_ACK_ENDPOINT" "$FAAS_SECRETS_RELOAD_GENERATION" > "$1"`, "main", output,
	}}
	sup := newSupervisorForMain(spec, manifest, initial, nil, nil)
	for _, current := range []map[string]string{{"DATABASE_URL": "rotated"}, {"DATABASE_URL": "rotated-again"}} {
		if err := spec.runtimeSecrets.publishForOwner(filepath.Join(dir, "projection", "secrets"), filepath.Join(dir, "projection", "revision"), os.Getuid(), os.Getuid(), os.Getgid(), current, strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
		if err := sup.Start(); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(output)
		want := current["DATABASE_URL"] + "|unset|" + metadataSecretReloadAckEndpoint + "|" + spec.runtimeSecrets.process.generation
		if err != nil || string(got) != want {
			t.Fatalf("main restart env = %q %v, want %q", got, err, want)
		}
	}
	if len(starts) != 2 || starts[0].Generation == starts[1].Generation || starts[1].PreviousGeneration != starts[0].Generation || spec.runtimeSecrets.process.active {
		t.Fatal("main restart reused or failed to retire execution identity")
	}
}

func TestMainWorkloadPreparesProjectionBeforeReload(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("guest projection preparation requires root ownership")
	}
	dir := t.TempDir()
	projection, revision := filepath.Join(dir, "main", "secrets.json"), filepath.Join(dir, "main", "revision")
	initial := map[string]string{"DATABASE_URL": "main-credential"}
	runtime, err := newMainWorkloadRuntime(workloadSpec{Name: "main", Type: "main"}, api.AppManifest{User: "0", SecretReloadSignal: "SIGUSR1"}, initial, nil, nil, nil, projection, revision)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.secretManifest == nil || runtime.spec.runtimeSecrets == nil || runtime.sup == nil {
		t.Fatal("main reload was not wired")
	}
	body, err := os.ReadFile(projection)
	var values map[string]string
	if err != nil || json.Unmarshal(body, &values) != nil || values["DATABASE_URL"] != initial["DATABASE_URL"] || len(values) != 1 {
		t.Fatalf("initial main projection = %s %v", body, err)
	}
	if body, err := os.ReadFile(revision); err != nil || len(body) != 0 {
		t.Fatalf("initial revision = %q %v", body, err)
	}
	initial["DATABASE_URL"] = "mutated"
	if runtime.spec.currentSecrets(initial)["DATABASE_URL"] != "main-credential" {
		t.Fatal("main runtime reused the wake-time map")
	}
	if info, err := os.Stat(projection); err != nil || info.Mode().Perm() != 0o400 {
		t.Fatalf("main projection privacy: %v %v", info, err)
	}
	// A restart-only main must neither publish files nor start a reloader.
	disabled, err := newMainWorkloadRuntime(workloadSpec{Name: "main", Type: "main"}, api.AppManifest{}, nil, nil, nil, nil, filepath.Join(dir, "disabled", "secrets"), filepath.Join(dir, "disabled", "revision"))
	if err != nil || disabled.secretManifest != nil || disabled.spec.runtimeSecrets != nil {
		t.Fatalf("disabled main = %+v %v", disabled, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "disabled")); !os.IsNotExist(err) {
		t.Fatalf("disabled main wrote a projection: %v", err)
	}
}
