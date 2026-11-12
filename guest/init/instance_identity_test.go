// adr: 20261107160000 — restored workloads read the serving instance's identity
// from a file guest-init rewrites on every resume.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func useInstanceIdentityPathForTest(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "guest-init", "identity.json")
	previousPath, previousEnabled := instanceIdentityPath, instanceIdentityEnabled.Load()
	instanceIdentityPath = path
	instanceIdentityEnabled.Store(false)
	t.Cleanup(func() {
		instanceIdentityPath = previousPath
		instanceIdentityEnabled.Store(previousEnabled)
	})
	return path
}

func readInstanceIdentityForTest(t *testing.T, path string) instanceIdentity {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read identity file: %v", err)
	}
	var got instanceIdentity
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode identity file %q: %v", data, err)
	}
	return got
}

func TestInstanceIdentityBootThenResume(t *testing.T) {
	path := useInstanceIdentityPathForTest(t)
	boot := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	apiEnv := map[string]string{
		api.PlatformInstanceIDEnv: "ins-capture", api.PlatformNodeIDEnv: "node-a", api.PlatformRegionEnv: "fsn",
		"LOG_LEVEL": "debug",
	}
	if err := publishBootInstanceIdentity(apiEnv, boot); err != nil {
		t.Fatal(err)
	}
	got := readInstanceIdentityForTest(t, path)
	want := instanceIdentity{InstanceID: "ins-capture", NodeID: "node-a", Region: "fsn", Source: "boot", UpdatedAt: boot.Format(time.RFC3339Nano)}
	if got != want {
		t.Fatalf("boot identity = %+v, want %+v", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o444 {
		t.Fatalf("identity file mode = %v, want 0444", info.Mode().Perm())
	}

	resumed := boot.Add(time.Hour)
	if err := publishResumedInstanceIdentity("ins-restored", "node-b", "hel", resumed); err != nil {
		t.Fatal(err)
	}
	got = readInstanceIdentityForTest(t, path)
	want = instanceIdentity{InstanceID: "ins-restored", NodeID: "node-b", Region: "hel", Source: "resume", UpdatedAt: resumed.Format(time.RFC3339Nano)}
	if got != want {
		t.Fatalf("resumed identity = %+v, want %+v", got, want)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("identity dir holds %d entries, want only identity.json (temp files must not leak)", len(entries))
	}
}

// Guests without a platform instance (builders, legacy fixtures) get no file
// and no variable, so no workload is pointed at a missing path.
func TestInstanceIdentityAbsentWithoutPlatformInstance(t *testing.T) {
	path := useInstanceIdentityPathForTest(t)
	if err := publishBootInstanceIdentity(map[string]string{"LOG_LEVEL": "info"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("identity file exists without a platform instance: %v", err)
	}
	if err := publishResumedInstanceIdentity("ins-restored", "node-b", "hel", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("resume wrote an identity file the boot never enabled: %v", err)
	}
	env := StampInstanceIdentityEnv([]string{"A=1"})
	if envValue(env, InstanceIdentityEnv) != "" {
		t.Fatalf("env = %v, want no %s", env, InstanceIdentityEnv)
	}
}

// An older vmmd sends no identity; the file keeps the last published one.
func TestInstanceIdentityResumeWithoutIdentityKeepsFile(t *testing.T) {
	path := useInstanceIdentityPathForTest(t)
	if err := publishBootInstanceIdentity(map[string]string{api.PlatformInstanceIDEnv: "ins-capture"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := publishResumedInstanceIdentity("", "", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := readInstanceIdentityForTest(t, path); got.InstanceID != "ins-capture" || got.Source != "boot" {
		t.Fatalf("identity = %+v, want the boot identity kept", got)
	}
}

// The platform owns the variable: a workload's own value is replaced.
func TestStampInstanceIdentityEnvReplacesWorkloadValue(t *testing.T) {
	path := useInstanceIdentityPathForTest(t)
	instanceIdentityEnabled.Store(true)
	env := StampInstanceIdentityEnv([]string{InstanceIdentityEnv + "=/tmp/spoofed", "A=1"})
	if got := envValue(env, InstanceIdentityEnv); got != path {
		t.Fatalf("%s = %q, want %q", InstanceIdentityEnv, got, path)
	}
	count := 0
	for _, kv := range env {
		if len(kv) > len(InstanceIdentityEnv) && kv[:len(InstanceIdentityEnv)+1] == InstanceIdentityEnv+"=" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("env carries %d %s entries, want 1: %v", count, InstanceIdentityEnv, env)
	}
}
