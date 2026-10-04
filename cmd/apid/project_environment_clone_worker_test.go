// adr:531
package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCloneWorkerRejectsUnboundedKernelResources(t *testing.T) {
	for _, limits := range [][3]string{
		{"100000 100000", "1073741824", "64"}, {"1000 100000", "268435456", "32"},
	} {
		if err := validateCloneWorkerResourceLimits(limits[0], limits[1], limits[2]); err != nil {
			t.Fatal(err)
		}
	}
	for _, limits := range [][3]string{
		{"max 100000", "1073741824", "64"}, {"100001 100000", "1073741824", "64"},
		{"18446744073709551615 100000", "1073741824", "64"}, {"100000 999", "1073741824", "64"},
		{"100000 1000001", "1073741824", "64"}, {"0 100000", "1073741824", "64"},
		{"100000 100000 extra", "1073741824", "64"}, {"100000 100000", "max", "64"},
		{"100000 100000", "1073741825", "64"}, {"100000 100000", "0", "64"},
		{"100000 100000", "1073741824", "max"}, {"100000 100000", "1073741824", "65"},
		{"100000 100000", "1073741824", "0"},
	} {
		if err := validateCloneWorkerResourceLimits(limits[0], limits[1], limits[2]); !errors.Is(err, managedpostgres.ErrConflict) {
			t.Fatalf("unsafe limits %v accepted: %v", limits, err)
		}
	}
}

func TestCloneWorkerAuthenticatesActualCgroupBeforeIO(t *testing.T) {
	for _, fault := range []string{"none", "public_apid", "legacy_hierarchy", "missing_cpu", "missing_memory", "missing_pids", "moved", "canceled"} {
		t.Run(fault, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			root := "/sys/fs/cgroup" + cloneWorkerCgroup + "/"
			files := map[string]string{"/proc/self/cgroup": "0::" + cloneWorkerCgroup + "\n", root + "cpu.max": "100000 100000\n", root + "memory.max": "1073741824\n", root + "pids.max": "64\n"}
			switch fault {
			case "public_apid":
				files["/proc/self/cgroup"] = "0::/faas-cp.slice/faas-apid.service\n"
			case "legacy_hierarchy":
				files["/proc/self/cgroup"] += "1:cpu:/old\n"
			case "missing_cpu":
				delete(files, root+"cpu.max")
			case "missing_memory":
				delete(files, root+"memory.max")
			case "missing_pids":
				delete(files, root+"pids.max")
			case "canceled":
				cancel()
			}
			reads, identities := 0, 0
			err := checkCloneWorkerResources(ctx, func(path string) ([]byte, error) {
				reads++
				if path == "/proc/self/cgroup" {
					identities++
					if fault == "moved" && identities == 2 {
						return []byte("0::/unbounded\n"), nil
					}
				}
				v, ok := files[path]
				if !ok {
					return nil, os.ErrNotExist
				}
				return []byte(v), nil
			})
			if fault == "none" {
				if err != nil || reads != 5 {
					t.Fatalf("qualified kernel admission: %v, reads %d", err, reads)
				}
				return
			}
			if err == nil {
				t.Fatal("lost kernel authority accepted")
			}
			if fault == "canceled" && (!errors.Is(err, context.Canceled) || reads != 0) {
				t.Fatal("canceled worker read kernel authority")
			}
			if (fault == "public_apid" || fault == "legacy_hierarchy") && reads != 1 {
				t.Fatal("foreign cgroup reached resource IO")
			}
		})
	}
}

type cloneWorkerClaimStore struct {
	*state.PgStore
	claims int
}

func (s *cloneWorkerClaimStore) ClaimNextProjectEnvironmentClone(context.Context, string, time.Duration) (state.ProjectEnvironmentCloneLease, error) {
	s.claims++
	return state.ProjectEnvironmentCloneLease{}, state.ErrNotFound
}

func TestCloneWorkerHostAdmissionPrecedesEveryDurableClaim(t *testing.T) {
	store := &cloneWorkerClaimStore{}
	srv := &server{store: store}
	err := srv.runProjectEnvironmentCloneCoordinatorWithAdmission(t.Context(), func(context.Context) error { return managedpostgres.ErrConflict }, nil)
	if !errors.Is(err, managedpostgres.ErrConflict) || store.claims != 0 {
		t.Fatalf("claim after rejected host admission: %v, %d", err, store.claims)
	}
	// An empty queue still beats the real loop; cancellation prevents another
	// claim and returns only after that owned iteration is terminal.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	admissions, beats := 0, 0
	err = srv.runProjectEnvironmentCloneCoordinatorWithAdmission(ctx, func(context.Context) error { admissions++; return nil }, func() { beats++; cancel() })
	if err != nil || store.claims != 1 || admissions != 1 || beats != 1 {
		t.Fatalf("queue lifetime: %v, %d/%d/%d", err, store.claims, admissions, beats)
	}
}

func TestCloneWorkerReadAdmissionRevokedBeforeNewOrActiveRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := copycontents.NewReadPool(dir, copycontents.ReadPoolLimits{Readers: 1, MemoryBytes: 32, DiskBytes: 64, MinFreeBytes: api.PostgresCopyContentsSpoolFreeReserveMin})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	}()
	srv := &server{clonePostgresContentsReadPool: p, cloneWorkerAdmission: func(context.Context) error { return managedpostgres.ErrConflict }}
	cfg := copycontents.Config{SpoolDir: dir, MaxBytes: 1024, SortMemoryBytes: 32, SortDiskBytes: 64}
	if c, release, err := srv.reserveProjectEnvironmentClonePostgresRead(t.Context(), cfg); !errors.Is(err, managedpostgres.ErrConflict) || release != nil || c.ReadPool != nil {
		t.Fatal("rejected CPU/RSS authority funded a read")
	}
	srv.cloneWorkerAdmission = p.CheckForWorker
	admitted, release, err := srv.reserveProjectEnvironmentClonePostgresRead(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := srv.authorizeProjectEnvironmentClonePostgresRead(t.Context(), admitted); err != nil {
		t.Fatal(err)
	}
	srv.cloneWorkerAdmission = func(context.Context) error { return managedpostgres.ErrConflict }
	if err := srv.authorizeProjectEnvironmentClonePostgresRead(t.Context(), admitted); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatal("active reader ignored lost host authority")
	}
}

func TestCloneWorkerKeysKeepRotationAndRejectPartialInstall(t *testing.T) {
	oldRecipient, oldIDs, oldHMAC := setSecretRecipient, mfaIdentities, hostHMACKey
	t.Cleanup(func() { setSecretRecipient, mfaIdentities, hostHMACKey = oldRecipient, oldIDs, oldHMAC })
	dir := t.TempDir()
	keys := []*age.X25519Identity{}
	for _, n := range []string{"faas_fleet_age_identity", "faas_host_age_identity", "faas_host_age_identity_previous"} {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, id)
		if err := os.WriteFile(filepath.Join(dir, n), []byte(id.String()), 0400); err != nil {
			t.Fatal(err)
		}
	}
	public := filepath.Join(dir, "faas_fleet_age_recipient")
	hmac := filepath.Join(dir, "faas_host_hmac_key")
	if err := os.WriteFile(public, []byte(keys[0].Recipient().String()), 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hmac, []byte(strings.Repeat("k", 32)), 0400); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"FAAS_FLEET_AGE_IDENTITY_PATH": filepath.Join(dir, "faas_fleet_age_identity"), "FAAS_FLEET_AGE_RECIPIENT_PATH": public, "FAAS_HOST_HMAC_KEY_PATH": hmac}
	getenv := func(k string) string { return env[k] }
	if err := loadProjectEnvironmentCloneWorkerKeys(getenv); err != nil {
		t.Fatal(err)
	}
	if setSecretRecipient().String() != keys[0].Recipient().String() || len(mfaIdentities()) != 3 || string(hostHMACKey()) != strings.Repeat("k", 32) {
		t.Fatal("original fleet and rotation credentials not retained")
	}
	if err := os.Chmod(public, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(public, []byte(keys[1].Recipient().String()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadProjectEnvironmentCloneWorkerKeys(getenv); err == nil {
		t.Fatal("substituted recipient accepted")
	}
	if setSecretRecipient().String() != keys[0].Recipient().String() || len(mfaIdentities()) != 3 {
		t.Fatal("failed key load partially replaced original identity")
	}
	delete(env, "FAAS_HOST_HMAC_KEY_PATH")
	if err := loadProjectEnvironmentCloneWorkerKeys(getenv); err == nil {
		t.Fatal("missing key admitted")
	}
}
