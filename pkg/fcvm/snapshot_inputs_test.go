package fcvm

// adr: 650
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func snapshotInputsFixture(t *testing.T) (*JailerVMM, Lease, SnapshotSpec, *SnapshotInputs, string) {
	t.Helper()
	jail := t.TempDir()
	if err := os.Chmod(jail, 0o700); err != nil {
		t.Fatal(err)
	}
	v := NewJailerVMM(jail, 0)
	l := Lease{Instance: "shape-capture", MemoryMaxMiB: 1}
	root := v.chrootRoot(l.Instance)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(root, "mem"))
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(f.Truncate(1<<20), f.Close()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "vmstate"), []byte("exact-vmstate"), 0o600); err != nil {
		t.Fatal(err)
	}
	drive := filepath.Join(t.TempDir(), "frozen-drive")
	if err := os.WriteFile(drive, []byte("exact-private-drive"), 0o400); err != nil {
		t.Fatal(err)
	}
	key := region.ArtifactKey("us-east1", state.SnapshotCaptureMemKey("deployment", "warm", "capture"))
	spec := SnapshotSpec{StorageKey: key, VMStateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/":
			fmt.Fprintf(w, `{"id":%q,"app_name":"Firecracker","vmm_version":"1.7.0","state":"Paused"}`, l.Instance)
		case "/machine-config":
			fmt.Fprint(w, `{"vcpu_count":2,"mem_size_mib":1,"smt":false}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := server.Client()
	client.Transport = &rewriteSnapshotTransport{url: server.URL, base: client.Transport}
	v.clients[l.Instance] = client
	machine, version, err := v.snapshotMachine(t.Context(), l)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := v.captureSnapshotInputs(t.Context(), l, spec, machine, version, drive)
	if err != nil {
		t.Fatal(err)
	}
	return v, l, spec, inputs, drive
}

type rewriteSnapshotTransport struct {
	url  string
	base http.RoundTripper
}

func (r *rewriteSnapshotTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.URL.Scheme = "http"
	copy.URL.Host = strings.TrimPrefix(r.url, "http://")
	return r.base.RoundTrip(copy)
}

func TestSnapshotInputsTravelWithRegionalBackingAndRefuseDifferentOriginalShape(t *testing.T) {
	v, l, spec, inputs, _ := snapshotInputsFixture(t)
	f := newBackingFixture(t.Context(), t)
	f.m.rememberInstanceBacking(l.Instance, f.base)
	f.m.writeSnapshotBacking(t.Context(), l.Instance, spec.StorageKey, inputs)
	if got := state.SnapshotBackingKey(state.Snapshot{StorageKey: spec.StorageKey}); got != strings.TrimSuffix(spec.StorageKey, "/mem")+"/backing" {
		t.Fatal("regional backing key lost its region", got)
	}
	identity, err := readSnapshotBacking(t.Context(), f.m.storage, spec.StorageKey)
	if err != nil || identity.Inputs == nil || *identity.Inputs != *inputs {
		t.Fatal(identity, err)
	}
	v.WithStorage(f.m.storage)
	owner := nativeLaunchRecord{Lease: l}
	v.nativeRecovery = &nativeProcessRecoveryRuntime{}
	restore := RestoreSpec{StorageKey: spec.StorageKey, VMStateStorageKey: spec.VMStateStorageKey, FCVersion: "1.7.0", ExpectedMachine: &inputs.Machine}
	if got, err := v.restoreSnapshotInputs(t.Context(), owner, l, restore); err != nil || *got != *inputs {
		t.Fatal(got, err)
	}
	for _, mutate := range []func(*RestoreSpec, *nativeLaunchRecord){
		func(s *RestoreSpec, _ *nativeLaunchRecord) { s.FCVersion = "1.8.0" },
		func(s *RestoreSpec, _ *nativeLaunchRecord) { s.VMStateStorageKey = "snap/other/vmstate" },
		func(s *RestoreSpec, _ *nativeLaunchRecord) {
			m := *s.ExpectedMachine
			m.VcpuCount++
			s.ExpectedMachine = &m
		},
		func(_ *RestoreSpec, o *nativeLaunchRecord) { o.Lease.MemoryMaxMiB++ },
		func(_ *RestoreSpec, o *nativeLaunchRecord) { o.RegionalRuntime = &runtimequota.Placement{} },
	} {
		s, o := restore, owner
		mutate(&s, &o)
		if _, err := v.restoreSnapshotInputs(t.Context(), o, l, s); err == nil {
			t.Fatal("incompatible original shape accepted")
		}
	}
}

func TestSnapshotInputsRequireCompleteCanonicalSidecar(t *testing.T) {
	_, _, spec, inputs, _ := snapshotInputsFixture(t)
	identity := BackingIdentity{Version: 1, Kernel: "kernel", Base: "base", Inputs: inputs}
	raw, _ := json.Marshal(identity)
	if _, err := decodeSnapshotBacking(raw); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string][]byte{
		"duplicate":        bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1),
		"unknown":          append([]byte(`{"extra":true,`), raw[1:]...),
		"null":             bytes.Replace(raw, []byte(`"machine":{`), []byte(`"machine":null,"machine":{`), 1),
		"trailing":         append(append([]byte(nil), raw...), []byte(`{}`)...),
		"alternate-casing": bytes.Replace(raw, []byte(`"memory"`), []byte(`"Memory"`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeSnapshotBacking(bad); err == nil {
				t.Fatal("ambiguous metadata accepted")
			}
		})
	}
	for name, mutate := range map[string]func(*SnapshotInputs){
		"RAM":       func(s *SnapshotInputs) { s.Machine.MemSizeMib++ },
		"CPU":       func(s *SnapshotInputs) { s.Machine.VcpuCount = 0 },
		"state-key": func(s *SnapshotInputs) { s.VMState.Key = "snap/other/vmstate" },
		"drive-key": func(s *SnapshotInputs) { s.Drive.Key = "snap/other/drive" },
		"digest":    func(s *SnapshotInputs) { s.Memory.SHA256 = strings.ToUpper(s.Memory.SHA256) },
	} {
		t.Run(name, func(t *testing.T) {
			s := *inputs
			mutate(&s)
			if s.Validate(spec.StorageKey) == nil {
				t.Fatal("invalid input shape accepted")
			}
		})
	}
}

func TestSnapshotInputFilesRefuseMutationBeforeLoadAndKeepOriginalDrive(t *testing.T) {
	for _, change := range []string{"none", "memory-bytes", "state-bytes", "drive-bytes", "size", "symlink", "replace-during-load", "symlink-during-load", "modify-during-load", "cancel"} {
		t.Run(change, func(t *testing.T) {
			v, l, _, inputs, drive := snapshotInputsFixture(t)
			root := v.chrootRoot(l.Instance)
			mem := filepath.Join(root, "mem")
			ctx := t.Context()
			var err error
			switch change {
			case "memory-bytes":
				err = os.WriteFile(mem, bytes.Repeat([]byte("x"), 1<<20), 0o600)
			case "state-bytes":
				err = os.WriteFile(filepath.Join(root, "vmstate"), []byte("other-vmstate"), 0o600)
			case "drive-bytes":
				err = os.Chmod(drive, 0o600)
				if err == nil {
					err = os.WriteFile(drive, []byte("other-private-drive"), 0o400)
				}
			case "size":
				err = os.Truncate(mem, 2<<20)
			case "symlink":
				err = os.Rename(mem, mem+"-original")
				if err == nil {
					err = os.Symlink(mem+"-original", mem)
				}
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err != nil {
				t.Fatal(err)
			}
			// Per-node resolver/environment updates apply to the private clone;
			// verify the immutable captured source, never its prepared clone.
			if err := os.WriteFile(filepath.Join(root, layerImageName), []byte("updated node resolver"), 0o600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			err = v.withSnapshotInputs(ctx, l, root, "mem", "vmstate", drive, inputs, func() error {
				calls++
				if change == "replace-during-load" || change == "symlink-during-load" {
					if err := os.Rename(mem, mem+"-original"); err != nil {
						return err
					}
					if change == "symlink-during-load" {
						return os.Symlink(mem+"-original", mem)
					}
					return os.WriteFile(mem, make([]byte, 1<<20), 0o600)
				}
				if change == "modify-during-load" {
					info, err := os.Stat(mem)
					if err != nil {
						return err
					}
					if err := os.WriteFile(mem, bytes.Repeat([]byte("x"), 1<<20), 0o600); err != nil {
						return err
					}
					return os.Chtimes(mem, info.ModTime(), info.ModTime())
				}
				return nil
			})
			if change == "none" {
				if err != nil || calls != 1 {
					t.Fatal(err, calls)
				}
				return
			}
			if err == nil {
				t.Fatal("changed snapshot accepted")
			}
			if !strings.HasSuffix(change, "during-load") && calls != 0 {
				t.Fatal("bad input reached load")
			}
		})
	}
}

func TestNativeSnapshotUnknownInputsRefuseBeforeStaging(t *testing.T) {
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	v := NewJailerVMM(t.TempDir(), 0).WithStorage(be)
	v.nativeRecovery = &nativeProcessRecoveryRuntime{}
	key := state.SnapshotCaptureMemKey("dep", "init", "capture")
	raw, _ := json.Marshal(BackingIdentity{Version: 1, Kernel: "kernel", Base: "base"})
	if err := be.Put(t.Context(), state.SnapshotBackingKey(state.Snapshot{StorageKey: key}), bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := v.restoreSnapshotInputs(t.Context(), nativeLaunchRecord{}, Lease{}, RestoreSpec{StorageKey: key}); !errors.Is(err, ErrSnapshotBackingUnverified) {
		t.Fatal(err)
	}
}

func TestNativeSnapshotInputsRecheckOriginalDimensionsAndBinaryBeforeLoad(t *testing.T) {
	for _, change := range []string{"RAM", "CPU", "binary"} {
		t.Run(change, func(t *testing.T) {
			v, lease, machine, _ := nativeMachineAPIFixture(t)
			inputs := &SnapshotInputs{Machine: machine.machine, FCVersion: "1.7.0"}
			switch change {
			case "RAM":
				inputs.Machine.MemSizeMib++
			case "CPU":
				inputs.Machine.VcpuCount++
			case "binary":
				machine.raw["GET /"] = fmt.Sprintf(`{"id":%q,"app_name":"Firecracker","vmm_version":"1.8.0","state":"Not started"}`, lease.Instance)
			}
			if err := v.loadSnapshot(t.Context(), lease, "state", "mem", true, inputs); err == nil {
				t.Fatal("incompatible input reached load")
			}
			machine.mu.Lock()
			loads := len(machine.loads)
			machine.mu.Unlock()
			if loads != 0 || machine.patchCount() != 0 {
				t.Fatal("native load/resume ran before original input checks")
			}
		})
	}
}

func TestSnapshotInputsCreatedBeforePublicationFromCompletedCapture(t *testing.T) {
	v, lease, spec, _, _ := snapshotInputsFixture(t)
	lease.Plan = api.PlanHobby
	cgroupDir := withFakeCgroupRoot(t)
	scope := filepath.Join(cgroupDir, ParentCgroupFor(lease.Plan), PerInstanceScope(lease.Instance))
	if err := os.MkdirAll(scope, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeAppCgroup(lease.Instance, lease.Plan, lease.MemoryMaxMiB, 1000); err != nil {
		t.Fatal(err)
	}
	fixture := newBackingFixture(t.Context(), t)
	v.WithStorage(fixture.m.storage)
	root := v.chrootRoot(lease.Instance)
	if err := os.WriteFile(filepath.Join(root, layerImageName), []byte("captured private drive"), 0o600); err != nil {
		t.Fatal(err)
	}
	var created atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /":
			fmt.Fprintf(w, `{"id":%q,"app_name":"Firecracker","vmm_version":"1.7.0","state":"Paused"}`, lease.Instance)
		case "GET /machine-config":
			fmt.Fprint(w, `{"vcpu_count":2,"mem_size_mib":1,"smt":false}`)
		case "PATCH /vm":
			w.WriteHeader(http.StatusNoContent)
		case "PUT /snapshot/create":
			created.Store(true)
			if err := os.WriteFile(filepath.Join(root, "vmstate"), []byte("completed new vmstate"), 0o600); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := server.Client()
	client.Transport = &rewriteSnapshotTransport{url: server.URL, base: client.Transport}
	v.clients[lease.Instance] = client
	info, err := v.SnapshotKeepAlive(t.Context(), lease, spec)
	if err != nil || !created.Load() || info.Inputs == nil {
		t.Fatal(info, err)
	}
	if info.Inputs.Memory.Bytes != info.MemBytes || info.Inputs.VMState.Bytes != info.VMStateBytes {
		t.Fatal("capture lengths lost original inputs")
	}
	fixture.m.rememberInstanceBacking(lease.Instance, fixture.base)
	fixture.m.writeSnapshotBacking(t.Context(), lease.Instance, spec.StorageKey, info.Inputs)
	backing, err := readSnapshotBacking(t.Context(), fixture.m.storage, spec.StorageKey)
	if err != nil || backing.Inputs == nil || *backing.Inputs != *info.Inputs {
		t.Fatal(backing, err)
	}
	for _, part := range []SnapshotArtifactIdentity{info.Inputs.Memory, info.Inputs.VMState, info.Inputs.Drive} {
		reader, err := fixture.m.storage.Get(t.Context(), part.Key)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		err = errors.Join(err, reader.Close())
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(data)
		if int64(len(data)) != part.Bytes || hex.EncodeToString(h[:]) != part.SHA256 {
			t.Fatal("published bytes differ from measured capture", part.Key)
		}
	}
}
