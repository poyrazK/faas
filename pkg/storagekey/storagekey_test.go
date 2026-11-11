package storagekey_test

import (
	goruntime "runtime"
	"testing"

	"github.com/onebox-faas/faas/pkg/storagekey"
)

// The layout is a wire and on-disk contract between schedd, vmmd, imaged
// and builderd: every key below names an object that already exists in
// production storage, so a change here strands it.
func TestLayout(t *testing.T) {
	const dep = "0b0e7a5c-0000-4000-8000-000000000001"
	cases := []struct {
		name, got, want string
	}{
		{"app layer", storagekey.AppLayer("shop", dep), "apps/shop/" + dep + ".ext4"},
		{"sidecar layer", storagekey.AppSidecarLayer("shop", dep, "redis"), "apps/shop/" + dep + "-redis.ext4"},
		{"legacy layer", storagekey.Layer(dep), "layers/" + dep + ".ext4"},
		{"job layer", storagekey.JobLayer("j1"), "jobs/j1.ext4"},
		{"job layer attempt", storagekey.JobLayerAttempt("j1", "a1"), "jobs/j1__a1.ext4"},
		{"snapshot mem", storagekey.SnapshotMem(dep), "snap/" + dep + "/mem"},
		{"snapshot vmstate", storagekey.SnapshotVMState(dep), "snap/" + dep + "/vmstate"},
		{"warm snapshot mem", storagekey.SnapshotWarmMem(dep), "snap/" + dep + "/warm/mem"},
		{"warm snapshot vmstate", storagekey.SnapshotWarmVMState(dep), "snap/" + dep + "/warm/vmstate"},
		{"plain base", storagekey.BaseForArch("", "amd64"), "base/base-amd64.ext4"},
		{"runner base", storagekey.BaseForArch("node22", "arm64"), "base/runner-node22-arm64.ext4"},
		{"binary shares the go124 base", storagekey.BaseForArch("binary", "amd64"), "base/runner-go124-amd64.ext4"},
		{"parent base", storagekey.BaseForArch(storagekey.ParentBaseRuntime, "amd64"), "base/runner-base-debian-parent-amd64.ext4"},
		{"agent base", storagekey.BaseForArch(storagekey.AgentRunBaseRuntime, "amd64"), "base/runner-agent-amd64.ext4"},
		{"agent layer", storagekey.AgentRunLayerForArch("amd64"), "base/agent-layer-v1-amd64.ext4"},
		{"sandbox layer", storagekey.SandboxLayerForArch("amd64"), "base/sandbox-layer-v1-amd64.ext4"},
		{"plain base digest", storagekey.BaseDigestForArch("", "amd64"), "base/base-amd64.ext4.digest"},
		{"runner base digest", storagekey.BaseDigestForArch("binary", "arm64"), "base/runner-go124-arm64.ext4.digest"},
		{"host arch base", storagekey.Base("builder"), "base/runner-builder-" + goruntime.GOARCH + ".ext4"},
		{"host arch base digest", storagekey.BaseDigest("builder"), "base/runner-builder-" + goruntime.GOARCH + ".ext4.digest"},
		{"kernel", storagekey.Kernel("v1.12.0"), "kernel/v1.12.0"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestIsParentBase(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"", false},
		{"base/runner-base-debian-parent-amd64.ext4", true},
		{"base/runner-base-debian-parent-arm64.ext4", true},
		{"base/runner-base-debian-parent-riscv64.ext4", false},
		{"base/runner-node22-amd64.ext4", false},
		{"apps/shop/x.ext4", false},
		{"base/runner-base-debian-parent-amd64.ext4.digest", false},
	}
	for _, tc := range cases {
		if got := storagekey.IsParentBase(tc.key); got != tc.want {
			t.Errorf("IsParentBase(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

func TestParentBaseAliasesStartWithTheHostArch(t *testing.T) {
	aliases := storagekey.ParentBaseAliases()
	if len(aliases) == 0 || aliases[0] != storagekey.BaseForArch(storagekey.ParentBaseRuntime, goruntime.GOARCH) {
		t.Fatalf("aliases %v do not start with the host arch's parent base", aliases)
	}
}

func TestIsSandboxTemplateLayer(t *testing.T) {
	const dep = "0b6f5c6e-3b0a-4f43-9a55-2f6c1b9d7e10"
	for key, want := range map[string]bool{
		storagekey.AppLayer("sbt-0123456789ab", dep): true,
		storagekey.AppLayer("sbt-0123456789", dep):   false,
		storagekey.AppLayer("myapp", dep):            false,
		storagekey.AppLayer("sbt-0123456789ab", "x"): false,
		"apps/sbt-0123456789ab/" + dep + "/x.ext4":   false,
		storagekey.JobLayer(dep):                     false,
	} {
		if got := storagekey.IsSandboxTemplateLayer(key); got != want {
			t.Errorf("IsSandboxTemplateLayer(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestIsJobLayer(t *testing.T) {
	const id, art = "0b6f5c6e-3b0a-4f43-9a55-2f6c1b9d7e10", "9c1a2b3c-4d5e-4f60-8a7b-0c1d2e3f4a5b"
	for key, want := range map[string]bool{
		storagekey.JobLayer(id):                               true,
		storagekey.JobLayerAttempt(id, art):                   true,
		"jobs/" + id + "__.ext4":                              false,
		"jobs/" + id:                                          false,
		"jobs/../" + id + ".ext4":                             false,
		"jobs/" + id + "/x.ext4":                              false,
		"apps/" + id + ".ext4":                                false,
		"jobs/" + "0B6F5C6E-3B0A-4F43-9A55-2F6C1B9D7E10.ext4": false,
		"": false,
	} {
		if got := storagekey.IsJobLayer(key); got != want {
			t.Errorf("IsJobLayer(%q) = %v, want %v", key, got, want)
		}
	}
}

// adr: 20261027090013 §12 — a preset's warm snapshot is shared, a
// template's is its account's own, and a template's builds share one slot.
func TestSandboxSnapshotKeys(t *testing.T) {
	preset := "snap/sandbox-preset/captures/abc/v2/mem"
	tpl := "snap/sandbox-template-acct/captures/abc/v2/mem"
	for _, tc := range []struct {
		key, account string
		image, ok    bool
	}{
		{preset, "acct", false, true},
		{preset, "acct", true, false},
		{tpl, "acct", true, true},
		{tpl, "other", true, false},
		{tpl, "acct", false, false},
		{"snap/sandbox-preset/captures/../v2/mem", "acct", false, false},
		{"snap/app-1/captures/abc/v2/mem", "acct", false, false},
		{"snap/sandbox-preset/captures/abc/v1/mem", "acct", false, false},
	} {
		if got := storagekey.IsSandboxSnapshotKey(tc.key, tc.account, tc.image); got != tc.ok {
			t.Errorf("storagekey.IsSandboxSnapshotKey(%q, %q, %v) = %v", tc.key, tc.account, tc.image, got)
		}
	}
	a := "apps/sbt-0123456789ab/11111111-1111-4111-8111-111111111111.ext4"
	b := "apps/sbt-0123456789ab/22222222-2222-4222-8222-222222222222.ext4"
	if storagekey.SandboxImageIdentity(a) != "apps/sbt-0123456789ab" || storagekey.SandboxImageIdentity(a) != storagekey.SandboxImageIdentity(b) ||
		storagekey.SandboxImageIdentity("jobs/j.ext4") != "jobs/j.ext4" {
		t.Fatal("a template's builds must share one identity, and other images keep their key")
	}
}
