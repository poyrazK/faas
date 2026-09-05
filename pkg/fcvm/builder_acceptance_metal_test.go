//go:build metal && linux

package fcvm_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderd"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
)

// TestMetalBuilderAcceptance drives the real builderd gRPC driver, jailed
// Firecracker, guest-init, BuildKit/Railpack, export, imaged conversion and a
// cold boot of the resulting app. Store and notification transport are in
// memory; PostgreSQL, scanner admission and scheduler snapshotting are separate
// gates. No fake VM or prebuilt customer output is used here.
func TestMetalBuilderAcceptance(t *testing.T) {
	if os.Getenv("FAAS_METAL_BUILD_ACCEPTANCE") != "1" {
		t.Skip("run make metal-lima-build for real builder acceptance")
	}
	for _, name := range []string{"FAAS_TEST_KERNEL", "FAAS_TEST_BASE_ROOTFS", "FAAS_TEST_VMMD_BINARY", "FAAS_GUEST_INIT", "FAAS_BUILDER_BASE_PATH"} {
		if _, err := os.Stat(os.Getenv(name)); err != nil {
			t.Fatalf("required %s: %v", name, err)
		}
	}
	selected, err := os.Stat(os.Getenv("FAAS_BUILDER_BASE_PATH"))
	mustAcceptance(t, err)
	canonical, err := os.Stat(filepath.Join("/srv/fc", sched.BaseKey("builder")))
	mustAcceptance(t, err)
	if !os.SameFile(selected, canonical) {
		t.Fatal("builder fixture must be staged at the canonical /srv/fc/base/runner-builder-<arch>.ext4 path used by vmmd")
	}
	if os.Geteuid() != 0 {
		t.Fatal("builder acceptance requires root on a dedicated KVM host")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Fatal(err)
	}
	buildTimeoutSeconds := api.BuildTimeoutSeconds
	if raw := os.Getenv("FAAS_METAL_BUILD_TIMEOUT_SECONDS"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 3600 {
			t.Fatal("FAAS_METAL_BUILD_TIMEOUT_SECONDS must be between 1 and 3600")
		}
		buildTimeoutSeconds = value
	}
	for _, tc := range []struct {
		name               string
		framework          builderd.Framework
		root               string
		cancel, fail, node bool
	}{
		{name: "dockerfile-executable", framework: builderd.FrameworkDocker},
		{name: "railpack-workspace", framework: builderd.FrameworkUnknown, root: "apps/api"},
		{name: "railpack-node-workspace", framework: builderd.FrameworkNode, root: "apps/api", node: true},
		{name: "failed-build", framework: builderd.FrameworkDocker, fail: true},
		{name: "cancel-running-build", framework: builderd.FrameworkDocker, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.node && os.Getenv("FAAS_METAL_NODE_ACCEPTANCE") != "1" {
				t.Skip("set FAAS_METAL_NODE_ACCEPTANCE=1 for the full cold Node toolchain gate")
			}
			m := fcvm.NewAcceptanceManager(t)
			tmp := t.TempDir()
			sock := filepath.Join(tmp, "v.sock")
			listener, err := net.Listen("unix", sock)
			mustAcceptance(t, err)
			server := grpc.NewServer()
			vmmdpb.RegisterVmmdServer(server, vmmdgrpc.New(acceptanceSignalAdapter{m}, nil, os.Getenv("FAAS_TEST_FC_VERSION"), slog.Default()))
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(server.Stop)
			driver, err := builderd.NewVMMDriver(sock, os.Getenv("FAAS_BUILDER_BASE_PATH"), filepath.Join(tmp, "drives"), filepath.Join(tmp, "exports"))
			mustAcceptance(t, err)
			t.Cleanup(func() { _ = driver.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(buildTimeoutSeconds+600)*time.Second)
			defer cancel()
			source := acceptanceSource(t, tmp, tc.root, acceptanceSourceOptions{node: tc.node, cancel: tc.cancel, fail: tc.fail})
			runtimeBaseRef := ""
			if tc.root != "" {
				runtimeBaseRef = acceptanceRuntimeBase(tc.node)
			}
			handle, err := driver.Spawn(ctx, builderd.VMRequest{BuildID: tc.name, TenantID: "metal", DeploymentID: tc.name, SourcePath: source, SourceRoot: tc.root, Framework: tc.framework, RuntimeBaseRef: runtimeBaseRef, Plan: string(api.PlanPro), TimeoutSec: buildTimeoutSeconds})
			mustAcceptance(t, err)
			t.Cleanup(func() {
				cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
				defer done()
				_ = driver.Cancel(cleanup, handle.BuildID)
				_ = m.Destroy(cleanup, handle.Instance)
				if m.LiveCount() != 0 || m.LeasedCount() != 0 {
					t.Error("VM or lease survived cleanup")
				}
				leakcheck.AssertZero(t)
			})
			if tc.cancel {
				waitAcceptanceLog(t, ctx, m, handle.Instance, "acceptance-build-running")
				finished := make(chan error, 1)
				go func() { _, err := driver.WaitForCompletion(ctx, handle); finished <- err }()
				// Destroy must already own export and have removed the live entry. This
				// specifically covers cancellation during the production wait/export RPC.
				deadline := time.Now().Add(5 * time.Second)
				for m.LiveCount() != 0 && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if m.LiveCount() != 0 {
					t.Fatal("Destroy did not begin waiting")
				}
				pid, alive := m.InstancePID(handle.Instance)
				if !alive {
					t.Fatal("builder exited before cancellation")
				}
				start := time.Now()
				stopCtx, stop := context.WithTimeout(ctx, 15*time.Second)
				defer stop()
				mustAcceptance(t, driver.Cancel(stopCtx, handle.BuildID))
				select {
				case err := <-finished:
					// A hard-killed guest may leave an unreadable in-progress
					// artifact. Cancellation promises bounded resource cleanup,
					// not a successful export of that interrupted filesystem.
					if err != nil {
						t.Logf("canceled build export: %v", err)
					}
				case <-stopCtx.Done():
					t.Fatal("canceled builder did not finish export/cleanup within 15s")
				}
				if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
					t.Fatalf("builder PID %d survived cancellation: %v", pid, err)
				}
				t.Logf("cancel plus export/cleanup: %s", time.Since(start))
			} else {
				result, err := driver.WaitForCompletion(ctx, handle)
				mustAcceptance(t, err)
				data, err := os.ReadFile(filepath.Join(handle.ExportDir, "build-done.json"))
				mustAcceptance(t, err)
				var done api.BuildDone
				mustAcceptance(t, json.Unmarshal(data, &done))
				t.Logf("guest build result: exit=%d\n%s", done.ExitCode, done.LogTail)
				if tc.fail {
					if result.ExitCode == 0 || done.ExitCode == 0 || !strings.Contains(done.LogTail, "exit code: 42") {
						t.Fatal("failing Dockerfile reported success")
					}
					if info, err := os.Stat(result.OCIImage); err == nil {
						if info.Size() != 0 {
							t.Fatalf("failed build exported nonempty image (%d bytes)", info.Size())
						}
					} else if !os.IsNotExist(err) {
						t.Fatal(err)
					}
				} else {
					if result.ExitCode != 0 || done.ExitCode != 0 {
						t.Fatalf("build failed: %+v", result)
					}
					acceptanceImageBoot(t, ctx, m, tmp, result.OCIImage)
				}
			}
			if m.LiveCount() != 0 || m.LeasedCount() != 0 {
				t.Fatal("VM or lease survived completion before fallback cleanup")
			}
			leakcheck.AssertZero(t)
			if _, err := os.Stat(handle.HostDrive1); !os.IsNotExist(err) {
				t.Fatalf("build scratch survived completion: %v", err)
			}
		})
	}
}

func mustAcceptance(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type acceptanceNotifier struct{ primed bool }

func (n *acceptanceNotifier) Notify(_ context.Context, channel, _ string) error {
	if channel == db.NotifySnapshotPrime {
		n.primed = true
	}
	return nil
}

func acceptanceImageBoot(t *testing.T, ctx context.Context, m *fcvm.Manager, tmp, archive string) {
	t.Helper()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "metal@example.com", api.PlanPro)
	mustAcceptance(t, err)
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "acceptance", Type: state.AppTypeApp, RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 1})
	mustAcceptance(t, err)
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball})
	mustAcceptance(t, err)
	mustAcceptance(t, store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployBuilding, ""))
	mustAcceptance(t, store.SetDeploymentRootfs(ctx, dep.ID, archive, "", 0))
	notifier := &acceptanceNotifier{}
	backend, err := storage.NewLocalStorageBackend(tmp)
	mustAcceptance(t, err)
	handler := imaged.New(store, notifier, nil, rootfs.NewBuilder(wire.ExecRunner{}), os.Getenv("FAAS_GUEST_INIT"), filepath.Join(tmp, "apps"), slog.Default()).WithStorage(backend)
	payload, err := json.Marshal(map[string]string{"app_id": app.ID, "deployment_id": dep.ID})
	mustAcceptance(t, err)
	handler.HandleNotification(ctx, db.Notification{Channel: db.NotifySnapshotBoot, Payload: string(payload)})
	dep, err = store.DeploymentByID(ctx, dep.ID)
	mustAcceptance(t, err)
	if dep.Status != state.DeploySnapshotting || !notifier.primed {
		t.Fatalf("imaged did not publish a bootable layer: status=%s error=%s", dep.Status, dep.Error)
	}
	instance, err := m.ColdBoot(ctx, fcvm.ColdBootRequest{Instance: "acceptance-app", Plan: api.PlanPro, BaseKey: os.Getenv("FAAS_TEST_BASE_ROOTFS"), LayerKey: dep.RootfsPath, VcpuCount: 2, MemSizeMiB: 256})
	mustAcceptance(t, err)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		mustAcceptance(t, m.Destroy(cleanup, instance.Lease.Instance))
	})
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s:8080/", instance.Lease.HostIP))
	mustAcceptance(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	mustAcceptance(t, err)
	if resp.StatusCode != 200 || strings.TrimSpace(string(body)) != "acceptance-ok" {
		t.Fatalf("built app response: %d %q", resp.StatusCode, body)
	}
	mustAcceptance(t, m.Destroy(ctx, instance.Lease.Instance))
}

func waitAcceptanceLog(t *testing.T, ctx context.Context, m *fcvm.Manager, id, marker string) {
	t.Helper()
	deadline, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ring := m.LogRing(id); ring != nil {
			for _, line := range ring.Snapshot(1) {
				if strings.Contains(line.Line, marker) {
					return
				}
			}
		}
		select {
		case <-deadline.Done():
			t.Fatalf("guest did not reach %q: %v", marker, deadline.Err())
		case <-ticker.C:
		}
	}
}

type acceptanceSourceOptions struct{ node, cancel, fail bool }

func acceptanceSource(t *testing.T, tmp, workspace string, opts acceptanceSourceOptions) string {
	t.Helper()
	files := map[string][]byte{}
	if workspace != "" {
		files["package.json"] = []byte(`{"name":"wrong-root","scripts":{"build":"exit 91"}}`)
		if opts.node {
			files[workspace+"/package.json"] = []byte(`{"name":"acceptance-api","version":"1.0.0","engines":{"node":"24"},"scripts":{"start":"node server.js"}}`)
			files[workspace+"/server.js"] = []byte(`require('http').createServer((q,s)=>s.end('acceptance-ok')).listen(8080,'0.0.0.0')`)
		} else {
			busybox, err := exec.LookPath("busybox")
			mustAcceptance(t, err)
			files[workspace+"/busybox"], err = os.ReadFile(busybox)
			mustAcceptance(t, err)
			files[workspace+"/start.sh"] = []byte("#!/bin/sh\nexec ./busybox httpd -f -p 8080 -h .\n")
			files[workspace+"/index.html"] = []byte("acceptance-ok\n")
			// Use Railpack's real shell provider and a small pinned build base.
			// This exercises prepare/frontend/RUN/export without downloading a
			// complete language toolchain for the default correctness gate.
			files[workspace+"/railpack.json"] = []byte(fmt.Sprintf(`{"provider":"shell","steps":{"build":{"inputs":[{"image":%q},{"local":true,"include":["."]}]}}}`, acceptanceRuntimeBase(false)))
		}
	} else {
		busybox, err := exec.LookPath("busybox")
		mustAcceptance(t, err)
		files["busybox"], err = os.ReadFile(busybox)
		mustAcceptance(t, err)
		script := "#!/busybox sh\n/busybox mkdir -p /www\necho acceptance-ok > /www/index.html\n"
		if opts.cancel {
			script = "#!/busybox sh\necho acceptance-build-running\n/busybox sleep 300\n"
		}
		if opts.fail {
			script = "#!/busybox sh\nexit 42\n"
		}
		files["build.sh"] = []byte(script)
		files["Dockerfile"] = []byte("FROM scratch\nCOPY busybox /busybox\nCOPY build.sh /build.sh\nRUN [\"/build.sh\"]\nEXPOSE 8080\nENTRYPOINT [\"/busybox\",\"httpd\",\"-f\",\"-p\",\"8080\",\"-h\",\"/www\"]\n")
	}
	path := filepath.Join(tmp, "source.tar.gz")
	f, err := os.Create(path)
	mustAcceptance(t, err)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		mode := int64(0644)
		if filepath.Base(name) == "busybox" || filepath.Base(name) == "start.sh" || name == "build.sh" {
			mode = 0755
		}
		mustAcceptance(t, tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), Typeflag: tar.TypeReg}))
		_, err = tw.Write(data)
		mustAcceptance(t, err)
	}
	mustAcceptance(t, tw.Close())
	mustAcceptance(t, gz.Close())
	mustAcceptance(t, f.Close())
	return path
}

// Same wire-to-syscall conversion as cmd/vmmd's signalAdapter.
type acceptanceSignalAdapter struct{ *fcvm.Manager }

func (a acceptanceSignalAdapter) SignalAndKill(ctx context.Context, id string, signal, grace int32) (bool, int32, error) {
	return a.Manager.SignalAndKill(ctx, id, syscall.Signal(signal), time.Duration(grace)*time.Second)
}

// Multi-arch parent indexes containing the repository's amd64 child pins in
// runner-node24.Dockerfile and base-minimal.Dockerfile, respectively. The shell
// fixture needs Bash because Railpack's frontend emits a /bin/bash entrypoint.
func acceptanceRuntimeBase(node bool) string {
	if node {
		return "node:24-bookworm-slim@sha256:ba849c60be29959425b8734d57b8b4b7d56f98edd9504c9af091d5281095a71e"
	}
	return "debian:12-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171"
}
