//go:build linux || darwin

package fcvm

// adr: 779
import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Synthetic ELF bytes model selected/copy identity only, not execution or KVM.
func nativeExecutableFixtureFile(t *testing.T, name string, body []byte) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, body, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNativeExecutableRetainsOriginalSelectedBytesBeforeAuthorization(t *testing.T) {
	j, lease, _, _ := preparedNativeRegionalResources(t)
	path := nativeExecutableFixtureFile(t, "firecracker", []byte("\x7fELF-original-source"))
	if err := j.captureExecutable(t.Context(), lease, path); err != nil {
		t.Fatal(err)
	}
	owner, err := j.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	before, err := j.readExecutable(owner)
	if err != nil || before == nil || owner.Authorized || before.Owner.Authorized || before.Bytes != 20 {
		t.Fatal(before, err)
	}
	if err := j.captureExecutable(t.Context(), lease, path); err != nil {
		t.Fatal("identical retry refused", err)
	}
	if err := os.WriteFile(path, []byte("\x7fELF-replaced-source"), 0o500); err != nil {
		t.Fatal(err)
	}
	if err := j.captureExecutable(t.Context(), lease, path); err == nil {
		t.Fatal("generation source was replaced")
	}
	after, err := j.readExecutable(owner)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("original source lost", err)
	}
	ticket, err := j.beginLaunch(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(ticket.authorize(42, 101), ticket.close()); err != nil {
		t.Fatal(err)
	}
	if err := j.captureExecutable(t.Context(), lease, path); err == nil {
		t.Fatal("observed running task backfilled original source")
	}
}

func TestNativeExecutableCaptureRefusesUnsafeOrCancelledSource(t *testing.T) {
	for _, scenario := range []string{"symlink", "writable", "not-executable", "script", "cancelled", "lease"} {
		t.Run(scenario, func(t *testing.T) {
			j, lease, _, _ := preparedNativeRegionalResources(t)
			path := nativeExecutableFixtureFile(t, "source", []byte("\x7fELF-original-source"))
			ctx := t.Context()
			switch scenario {
			case "symlink":
				link := filepath.Join(filepath.Dir(path), "link")
				if err := os.Symlink(path, link); err != nil {
					t.Fatal(err)
				}
				path = link
			case "writable":
				if err := os.Chmod(path, 0o777); err != nil {
					t.Fatal(err)
				}
			case "not-executable":
				if err := os.Chmod(path, 0o400); err != nil {
					t.Fatal(err)
				}
			case "script":
				if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o500); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "lease":
				lease.MemoryMaxMiB++
			}
			if err := j.captureExecutable(ctx, lease, path); err == nil {
				t.Fatal("unsafe source granted provenance")
			}
		})
	}
}

func TestNativeExecutableRecordRefusesDamageOrForeignOriginalBinding(t *testing.T) {
	for _, scenario := range []string{"missing", "duplicate", "null", "unknown", "owner", "digest", "oversized", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			v, lease, _, _ := nativeMachineAPIFixture(t)
			j := v.nativeRecovery.journal
			owner, err := j.read(lease.Instance)
			if err != nil {
				t.Fatal(err)
			}
			path := j.executablePath(owner)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				copy := filepath.Join(filepath.Dir(path), "copy")
				if err := os.WriteFile(copy, raw, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(copy, path); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				raw = append([]byte(`{"version":1,`), raw[1:]...)
			case "null":
				raw = []byte(`{"version":1,"owner":null,"bytes":1,"sha256":"a"}`)
			case "unknown":
				raw = append([]byte(`{"unknown":1,`), raw[1:]...)
			case "oversized":
				raw = []byte(strings.Repeat(" ", api.NativeExecutableRecordMaxBytes+1))
			case "owner", "digest":
				var record nativeExecutableRecord
				if err := json.Unmarshal(raw, &record); err != nil {
					t.Fatal(err)
				}
				if scenario == "owner" {
					record.Owner.Lease.MemoryMaxMiB++
				} else {
					record.SHA256 = strings.ToUpper(record.SHA256)
				}
				raw, err = json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "missing" && scenario != "symlink" {
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := v.withNativeMachineAPI(t.Context(), lease, func(*nativeMachineAPI) error { t.Fatal("damaged source admitted producer"); return nil }); err == nil {
				t.Fatal("invalid original source accepted")
			}
		})
	}
}

func TestNativeMachineSessionPinsOriginalCopiedExecutable(t *testing.T) {
	for _, scenario := range []string{"unchanged", "copied-wrong", "replaced", "modified", "source-removed", "source-changed", "exit", "during-request"} {
		t.Run(scenario, func(t *testing.T) {
			v, lease, f, handle := nativeMachineAPIFixture(t)
			j := v.nativeRecovery.journal
			owner, err := j.read(lease.Instance)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(v.nativeRecovery.retirer.probe.root, strconv.Itoa(owner.PID), "exe")
			if scenario == "copied-wrong" {
				target, err := os.Readlink(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte("\x7fELF-different-copy"), 0o500); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "during-request" {
				f.hook = func(string) {
					copy := nativeExecutableFixtureFile(t, "replacement", []byte("\x7fELF-original-firecracker-fixture"))
					if err := os.Remove(path); err != nil {
						t.Error(err)
						return
					}
					if err := os.Symlink(copy, path); err != nil {
						t.Error(err)
					}
				}
			}
			var retained *os.File
			err = v.withNativeMachineAPI(t.Context(), lease, func(s *nativeMachineAPI) error {
				retained = s.executableFile
				if retained == nil || s.executable == nil {
					t.Fatal("running executable not pinned")
				}
				switch scenario {
				case "replaced":
					copy := nativeExecutableFixtureFile(t, "replacement", []byte("\x7fELF-original-firecracker-fixture"))
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(copy, path); err != nil {
						t.Fatal(err)
					}
				case "modified":
					target, err := os.Readlink(path)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(target, []byte("\x7fELF-mutated-original"), 0o500); err != nil {
						t.Fatal(err)
					}
				case "source-removed":
					if err := os.Remove(j.executablePath(owner)); err != nil {
						t.Fatal(err)
					}
				case "source-changed":
					record := *s.executableSource
					record.SHA256 = strings.Repeat("ab", 32)
					if err := writeNativeJournalValue(j.executablePath(owner), record); err != nil {
						t.Fatal(err)
					}
				case "exit":
					handle.setLiveError(errors.New("exited"))
				}
				_, err := s.request(t.Context(), http.MethodPatch, "/vm", map[string]any{"state": "Resumed"}, http.StatusNoContent)
				return err
			})
			if (err == nil) != (scenario == "unchanged") {
				t.Fatalf("identity change admitted: %v", err)
			}
			if retained != nil {
				if _, err := retained.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Fatal("executable descriptor leaked", err)
				}
			}
			f.mu.Lock()
			requests := len(f.requests)
			f.mu.Unlock()
			if scenario == "during-request" && requests != 1 {
				t.Fatal("ambiguous request replayed", requests)
			}
			if scenario != "unchanged" && scenario != "during-request" && requests != 0 {
				t.Fatal("request sent after executable identity change")
			}
		})
	}
}

func TestNativeRuntimeInventoryKeepsMissingExecutableUnknownAndClonesKnownInput(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(strconv.FormatBool(missing), func(t *testing.T) {
			v, lease, _, _, _ := nativeRuntimeFixture(t)
			owner, err := v.nativeRecovery.journal.read(lease.Instance)
			if err != nil {
				t.Fatal(err)
			}
			if missing {
				if err := os.Remove(v.nativeRecovery.journal.executablePath(owner)); err != nil {
					t.Fatal(err)
				}
			}
			err = v.withNativeRuntimeInventory(t.Context(), func(_ context.Context, sample NativeRuntimeInventory) error {
				if len(sample.Owners) != 1 {
					t.Fatal("owner lost")
				}
				observed := sample.Owners[0]
				if missing {
					if observed.Executable != nil || observed.Unresolved == "" || observed.RegionalRuntimeCompatibility() == "" {
						t.Fatal("historical executable acquired authority")
					}
				} else {
					if observed.Executable == nil || observed.Unresolved != "" {
						t.Fatal("original copied executable unobserved", observed.Unresolved)
					}
					clone := cloneNativeRuntimeInventory(sample)
					clone.Owners[0].Executable.Inode++
					if *clone.Owners[0].Executable == *sample.Owners[0].Executable {
						t.Fatal("consumer aliases original observation")
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
