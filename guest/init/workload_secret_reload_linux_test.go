//go:build linux

// adr: 434

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSidecarRuntimeEnvRemovesRevokedKeysFromEveryLayer(t *testing.T) {
	for _, overrides := range []map[string]string{
		{"DATABASE_URL": "wake-db", "REVOKED_TOKEN": "wake-token", "FEATURE": "on"}, nil,
	} {
		base := []string{"DATABASE_URL=image-db", "REVOKED_TOKEN=image-token", "PATH=/bin"}
		spec := workloadSpec{GrantedEnvNames: []string{"DATABASE_URL", "REVOKED_TOKEN"},
			runtimeSecrets: newRuntimeSecretsState(map[string]string{"DATABASE_URL": "rotated", "UNGRANTED": "hidden"})}
		before := cloneRuntimeSecrets(overrides)
		env := applySidecarRuntimeEnv(base, overrides, spec)
		joined := "\n" + strings.Join(env, "\n") + "\n"
		if !strings.Contains(joined, "\nDATABASE_URL=rotated\n") || strings.Contains(joined, "\nREVOKED_TOKEN=") || strings.Contains(joined, "\nUNGRANTED=") || !strings.Contains(joined, "\nPATH=/bin\n") {
			t.Fatalf("restart env retained a revoked/ungranted value: %v", env)
		}
		if overrides != nil && !strings.Contains(joined, "\nFEATURE=on\n") {
			t.Fatal("non-secret override was removed")
		}
		if !reflect.DeepEqual(before, cloneRuntimeSecrets(overrides)) || base[1] != "REVOKED_TOKEN=image-token" {
			t.Fatal("restart mutated its wake-time inputs")
		}
	}
}

func TestSidecarRuntimePreservesReloadAcrossProcessRestarts(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("sidecar chroot and projection owner require root")
	}
	root, image := sidecarReloadFixture(t)
	spec := workloadSpec{Name: "worker", Type: "sidecar", Essential: true, GrantedEnvNames: []string{"DATABASE_URL", "REVOKED_TOKEN"}}
	runtime, err := newSidecarWorkloadRuntimeAt(root, spec, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var starts []runtimeConfigRequest
	runtime.spec.runtimeSecrets.process.transport = func(req runtimeConfigRequest) error {
		if req.Kind == "secret_generation_start" {
			starts = append(starts, req)
		}
		return nil
	}
	projection, revision := sidecarSecretReloadProjectionPaths(spec.Name, image)
	if body, err := os.ReadFile(revision); err != nil || len(body) != 0 {
		t.Fatalf("initial revision not prepared before process start: %q %v", body, err)
	}
	// A fetch can finish while dependencies delay the first process start.
	// Each subsequent Start reads that projection without resetting it.
	for i, current := range []map[string]string{{"DATABASE_URL": "rotated", "REVOKED_TOKEN": "new-token"}, {"DATABASE_URL": "next"}, {}} {
		version := strings.Repeat(string(rune('a'+i)), 64)
		if err := runtime.spec.runtimeSecrets.publishProjection(current, version); err != nil {
			t.Fatal(err)
		}
		if err := runtime.sup.Start(); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(filepath.Join(image, "tmp", "result"))
		if err != nil {
			t.Fatal(err)
		}
		db, token := "unset", "unset"
		if value, ok := current["DATABASE_URL"]; ok {
			db = value
		}
		if value, ok := current["REVOKED_TOKEN"]; ok {
			token = value
		}
		snapshotJSON, err := json.Marshal(runtimeSecretSnapshot{Revision: version, Secrets: current})
		if err != nil {
			t.Fatal(err)
		}
		want := db + "|" + token + "|" + version + "|" + metadataSecretReloadAckEndpoint + "?workload=worker|on" + "|" + string(snapshotJSON)
		if string(body) != want {
			t.Fatalf("process %d env/revision = %q %v, want %q", i, body, err, want)
		}
		for _, path := range []string{projection, revision, filepath.Join(filepath.Dir(projection), secretReloadSnapshotFileName)} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0400 || info.Sys().(*syscall.Stat_t).Uid != 43210 {
				t.Fatalf("image-local projection owner changed: %v %v", info, err)
			}
		}
	}
	if len(starts) != 3 || starts[0].Generation == starts[1].Generation || starts[1].PreviousGeneration != starts[0].Generation || starts[2].PreviousGeneration != starts[1].Generation || runtime.spec.runtimeSecrets.process.active {
		t.Fatal("sidecar restart reused or failed to retire execution identity")
	}
}

func TestPreparedSidecarReloaderUsesImageOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("projection owner requires root")
	}
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "rotated"}[changed], func(t *testing.T) {
			root, image := sidecarReloadFixture(t)
			spec := workloadSpec{Name: "worker", Type: "sidecar", GrantedEnvNames: []string{"DATABASE_URL", "REVOKED_TOKEN"}}
			runtime, err := newSidecarWorkloadRuntimeAt(root, spec, nil, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			fresh := runtime.spec.runtimeSecrets.snapshot()
			if changed {
				fresh = map[string]string{"DATABASE_URL": "rotated"}
			}
			version := strings.Repeat("d", 64)
			previous := dialRuntimeConfigHost
			t.Cleanup(func() { dialRuntimeConfigHost = previous })
			reported := make(chan runtimeConfigRequest, 1)
			dialRuntimeConfigHost = func() (net.Conn, error) {
				client, server := net.Pipe()
				go func() {
					defer server.Close()
					frame, err := readRuntimeConfigFrame(server)
					var request runtimeConfigRequest
					if err != nil || json.Unmarshal(frame, &request) != nil {
						return
					}
					response := runtimeConfigResponse{Accepted: true}
					if request.Kind == "secrets" {
						response = runtimeConfigResponse{Secrets: &fresh, Revision: version}
					}
					body, _ := json.Marshal(response)
					_ = writeRuntimeConfigFrame(server, body)
					if request.Kind == "secret_reload_status" {
						reported <- request
					}
				}()
				return client, nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := startRuntimeSecretReloaderForWorkload(ctx, *runtime.secretManifest, runtime.spec.runtimeSecrets, runtime.sup, nil, spec.Name)
			t.Cleanup(func() { cancel(); <-done })
			select {
			case request := <-reported:
				wantProjection, wantSignal := "unchanged", "not_attempted"
				if changed {
					wantProjection, wantSignal = "updated", "queued"
				}
				if request.WorkloadName != "worker" || request.Revision != version || request.Projection != wantProjection || request.Signal != wantSignal {
					t.Fatalf("report=%+v", request)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("reload did not report")
			}
			projection, revision := sidecarSecretReloadProjectionPaths(spec.Name, image)
			for _, path := range []string{projection, revision, filepath.Join(filepath.Dir(projection), secretReloadSnapshotFileName)} {
				info, err := os.Stat(path)
				if err != nil || info.Sys().(*syscall.Stat_t).Uid != 43210 || info.Mode().Perm() != 0400 {
					t.Fatalf("reload owner/privacy: %v %v", info, err)
				}
			}
			if body, err := os.ReadFile(revision); err != nil || string(body) != version {
				t.Fatalf("revision=%q %v", body, err)
			}
		})
	}
}

func TestSidecarRuntimePreparationHonorsOptInAndFailsClosed(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("projection owner requires root")
	}
	for _, scenario := range []string{"disabled", "init", "no_grants", "missing_grant", "invalid_owner", "blocked_projection"} {
		t.Run(scenario, func(t *testing.T) {
			root, image := sidecarReloadFixture(t)
			spec := workloadSpec{Name: "worker", Type: "sidecar", GrantedEnvNames: []string{"DATABASE_URL"}}
			manifest, err := loadSidecarManifestAt(image, spec.Name)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "disabled":
				manifest.SecretReloadSignal = ""
			case "init":
				spec.Type = "init"
			case "no_grants":
				spec.GrantedEnvNames = nil
			case "missing_grant":
				spec.GrantedEnvNames = []string{"MISSING"}
			case "invalid_owner":
				manifest.User = "reload-user:missing-group"
			case "blocked_projection":
				path := filepath.Join(image, strings.TrimPrefix(filepath.Dir(secretReloadFilePath), "/"))
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("blocked"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var encoded bytes.Buffer
			if err := api.WriteManifest(&encoded, manifest); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(image, "etc/faas/workloads/worker/workload.json"), encoded.Bytes(), 0644); err != nil {
				t.Fatal(err)
			}
			runtime, err := newSidecarWorkloadRuntimeAt(root, spec, nil, nil, nil, nil)
			switch scenario {
			case "missing_grant", "invalid_owner", "blocked_projection":
				if err == nil || runtime != nil {
					t.Fatalf("invalid preparation returned runtime=%+v err=%v", runtime, err)
				}
			default:
				if err != nil || runtime.secretManifest != nil || runtime.spec.runtimeSecrets != nil {
					t.Fatalf("opt-out prepared a reloader: %+v %v", runtime, err)
				}
				projection, _ := sidecarSecretReloadProjectionPaths(spec.Name, image)
				if _, err := os.Stat(projection); !os.IsNotExist(err) {
					t.Fatalf("opt-out published secrets: %v", err)
				}
			}
		})
	}
}

func sidecarReloadFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	image := filepath.Join(root, strings.TrimPrefix(api.FullRootfsSidecarMountPath, "/"), "worker", "upper")
	for _, sub := range []string{"bin", "etc/faas/workloads/worker", "tmp"} {
		if err := os.MkdirAll(filepath.Join(image, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(image, "tmp"), 0777); err != nil {
		t.Fatal(err)
	}
	if err := writeSidecarMountMarker(root); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{"passwd": "reload-user:x:43210:43211::/:/bin/sh\n", "group": "reload-group:x:43211:\n"} {
		if err := os.WriteFile(filepath.Join(image, "etc", name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	busybox, err := exec.LookPath("busybox")
	if err != nil {
		t.Skip("static busybox required for the isolated process fixture")
	}
	body, err := os.ReadFile(busybox)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(image, "bin", "busybox"), body, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sh", "cat"} {
		if err := os.Symlink("/bin/busybox", filepath.Join(image, "bin", name)); err != nil {
			t.Fatal(err)
		}
	}
	manifest := api.AppManifest{User: "reload-user:reload-group", SecretReloadSignal: "SIGHUP", WorkingDir: "/", Env: map[string]string{"DATABASE_URL": "image-db", "REVOKED_TOKEN": "image-token"}, Entrypoint: []string{"/bin/sh", "-c", `printf '%s|%s|%s|%s|%s|%s' "${DATABASE_URL-unset}" "${REVOKED_TOKEN-unset}" "$(cat "$FAAS_SECRETS_REVISION_FILE")" "$FAAS_SECRETS_RELOAD_ACK_ENDPOINT" "$FEATURE" "$(cat "$FAAS_SECRETS_SNAPSHOT_FILE")" > /tmp/result`}}
	var encoded bytes.Buffer
	if err := api.WriteManifest(&encoded, manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(image, "etc/faas/workloads/worker/workload.json"), encoded.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, strings.TrimPrefix(api.SidecarWorkloadManifestPath, "/"), "worker", "env.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"DATABASE_URL":"wake-db","REVOKED_TOKEN":"wake-token","FEATURE":"on"}`), 0400); err != nil {
		t.Fatal(err)
	}
	return root, image
}
