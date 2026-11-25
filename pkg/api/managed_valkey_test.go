package api

import (
	"slices"
	"strings"
	"testing"
)

func TestManagedValkeyPlanSizes(t *testing.T) {
	for _, tc := range []struct {
		plan        Plan
		caches, max int
	}{
		{PlanFree, 0, 0},
		{PlanHobby, 1, 448},
		{PlanPro, 3, 512},
		{PlanScale, 10, 512},
	} {
		if got := ManagedValkeyCachesFor(tc.plan); got != tc.caches {
			t.Errorf("%s caches = %d, want %d", tc.plan, got, tc.caches)
		}
		if got := ManagedValkeyMaxMemoryFor(tc.plan); got != tc.max {
			t.Errorf("%s max memory = %d, want %d", tc.plan, got, tc.max)
		}
		if tc.max > 0 {
			limits := MustLimitsFor(tc.plan)
			if ManagedValkeyVMRAMMB(tc.max) > limits.RAMMB {
				t.Errorf("%s: largest cache VM %d MB exceeds plan RAM %d MB", tc.plan, ManagedValkeyVMRAMMB(tc.max), limits.RAMMB)
			}
		}
	}
}

func TestValidateManagedValkeyMemory(t *testing.T) {
	if p := ValidateManagedValkeyMemory(PlanPro, 512); p != nil {
		t.Fatalf("512 on pro: %v", p.Detail)
	}
	for _, mb := range []int{0, 16, 100, 544} {
		if ValidateManagedValkeyMemory(PlanPro, mb) == nil {
			t.Errorf("%d MB accepted", mb)
		}
	}
	if p := ValidateManagedValkeyMemory(PlanFree, 64); p == nil || p.Code != CodeManagedRedisNotInPlan {
		t.Fatalf("free: %+v", p)
	}
}

func TestManagedValkeyServerCommand(t *testing.T) {
	argv := ManagedValkeyServerCommand(128, "allkeys-lfu")
	if len(argv) != 3 || argv[0] != "/bin/sh" || argv[1] != "-c" {
		t.Fatalf("argv = %q", argv)
	}
	script := argv[2]
	for _, want := range []string{
		"exec valkey-server", "--port 6379", "--maxmemory 128mb", "--maxmemory-policy allkeys-lfu",
		"--save ''", "--appendonly no", "--rename-command DEBUG ''",
		"--rename-command MONITOR ''",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q: %s", want, script)
		}
	}
	if !strings.HasPrefix(ManagedValkeyImage, "docker.io/valkey/valkey@") || !strings.Contains(ManagedValkeyImage, "@sha256:") {
		t.Fatalf("image is not digest-pinned: %s", ManagedValkeyImage)
	}
}

func TestManagedValkeyURL(t *testing.T) {
	if got := ManagedValkeyURL("sessions"); got != "redis://sessions.svc.gregale:6379" {
		t.Fatalf("url = %s", got)
	}
	if got := ManagedValkeyAddr("sessions"); got != "sessions.svc.gregale:6379" {
		t.Fatalf("addr = %s", got)
	}
	if strings.Contains(ManagedValkeyServerCommand(64, "noeviction")[2], "requirepass") {
		t.Fatal("cache must run without a password")
	}
}

func TestManagedValkeyHandoffCommand(t *testing.T) {
	argv := ManagedValkeyHandoffCommand(ManagedValkeyHost("sessions"), 256, "allkeys-lru", 1_900_000_000)
	if len(argv) != 3 || argv[0] != "/bin/sh" || argv[1] != "-c" {
		t.Fatalf("argv = %q", argv)
	}
	script := argv[2]
	for _, want := range []string{
		`[ "$(date +%s)" -ge 1900000000 ]`,      // after the window: plain start
		"--port 0 ",                             // TCP closed while copying
		"--replicaof sessions.svc.gregale 6379", // copy the running cache
		"CLIENT PAUSE 300000 WRITE",             // freeze writes before the switch
		"REPLICAOF NO ONE",                      // promote
		"CONFIG SET port 6379",                  // open: readiness passes
		"CLIENT UNPAUSE",                        // a failed handoff lifts the pause
		"--maxmemory 256mb",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("handoff script lacks %q", want)
		}
	}
	// The port opens last: readiness must not pass before the promotion.
	if strings.Index(script, "CONFIG SET port") < strings.Index(script, "REPLICAOF NO ONE") {
		t.Fatal("port opens before promotion")
	}
}

func TestManagedValkeyCommandFences(t *testing.T) {
	for _, platform := range []string{"REPLICAOF", "PSYNC", "SYNC", "CONFIG"} {
		if slices.Contains(ManagedValkeyBlockedCommands, platform) {
			t.Errorf("%s is renamed away, but the handoff needs it", platform)
		}
		if !slices.Contains(ManagedValkeyClientDeniedCommands, platform) {
			t.Errorf("%s must be refused for customer sessions", platform)
		}
	}
}

func TestManagedValkeyImageIsADeployableReference(t *testing.T) {
	if !ValidDeploymentImage(ManagedValkeyImage) {
		t.Fatalf("deploy admission rejects %s", ManagedValkeyImage)
	}
}

// Every image a cache may run is a digest-pinned, deployable Valkey
// reference, and the one apid deploys is among them, so imaged and schedd
// recognize every cache apid creates.
func TestManagedValkeyImagesAreDeployableAndIncludeTheDeployedOne(t *testing.T) {
	if !IsManagedValkeyImage(ManagedValkeyImage) {
		t.Fatalf("%s is not an accepted cache image", ManagedValkeyImage)
	}
	seen := map[string]bool{}
	for _, image := range ManagedValkeyImages() {
		if seen[image] {
			t.Errorf("duplicate cache image %s", image)
		}
		seen[image] = true
		if !strings.HasPrefix(image, "docker.io/valkey/valkey@sha256:") || !ValidDeploymentImage(image) {
			t.Errorf("cache image %s is not a digest-pinned deployable Valkey reference", image)
		}
	}
	for _, other := range []string{"", "docker.io/valkey/valkey:8.1.12-alpine", "docker.io/library/redis@sha256:9971099af41c36e887aabdfe22b80c0fe45df949163dfc9cb434febc57cfc3fc"} {
		if IsManagedValkeyImage(other) {
			t.Errorf("%q accepted as a cache image", other)
		}
	}
}

// apid's cache commands identify a cache deployment; a customer's own Valkey
// command on the same image does not.
func TestIsManagedValkeyCommand(t *testing.T) {
	for _, tc := range []struct {
		name       string
		entrypoint []string
		want       bool
	}{
		{"server", ManagedValkeyServerCommand(128, "allkeys-lru"), true},
		{"handoff", ManagedValkeyHandoffCommand("10.200.0.7", 256, "noeviction", 1<<40), true},
		{"customer argv", []string{"valkey-server", "--port", "6379"}, false},
		{"customer shell", []string{"/bin/sh", "-c", "exec valkey-server /etc/valkey.conf"}, false},
		{"image default", nil, false},
	} {
		if got := IsManagedValkeyCommand(tc.entrypoint); got != tc.want {
			t.Errorf("%s: IsManagedValkeyCommand = %v, want %v", tc.name, got, tc.want)
		}
	}
}
