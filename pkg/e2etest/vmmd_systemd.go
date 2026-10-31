package e2etest

// vmmd under systemd (FAAS_E2E_VMMD_SYSTEMD=1).
//
// Production runs vmmd as faas-vmmd.service. Its sandbox (ProtectSystem=strict,
// ProtectHome, ReadWritePaths, …) gives vmmd a private mount namespace, so the
// mounts it makes there are invisible to PID 1 and to every other service. The
// harness normally runs vmmd as its own child in the host's namespace, which
// hid a production-only bug: on rc.305 every instance bridge unit failed to
// open its guest's /run/netns/fc-<id> (EINVAL), because systemd could not see
// the namespace vmmd had bound there. In this mode vmmd runs as a transient
// service carrying the production unit's sandbox lines, so a metal test fails
// the way production would.

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
)

const (
	vmmdSystemdEnv = "FAAS_E2E_VMMD_SYSTEMD"
	vmmdUnitPrefix = "faas-e2e-vmmd-"
)

// VMMDUnderSystemd reports whether the harness runs vmmd as a transient
// service carrying the production unit's sandbox.
func VMMDUnderSystemd() bool { return os.Getenv(vmmdSystemdEnv) == "1" }

// vmmdSandboxKeys are the production unit's [Service] settings that shape
// vmmd's mount namespace, privileges and filesystem view, and how it stops.
var vmmdSandboxKeys = map[string]bool{
	"ProtectSystem": true, "ProtectHome": true, "ReadWritePaths": true, "PrivateTmp": true,
	"NoNewPrivileges": true, "CapabilityBoundingSet": true, "AmbientCapabilities": true, "Delegate": true,
	"ProtectKernelModules": true, "ProtectKernelTunables": true, "ProtectControlGroups": true,
	"SystemCallArchitectures": true, "LockPersonality": true, "RestrictRealtime": true, "RestrictSUIDSGID": true,
	"RestrictAddressFamilies": true, "ProtectHostname": true, "ProtectClock": true, "KillMode": true,
	// /run/faas-ibridge holds the bridge sockets; ProtectSystem=strict leaves
	// it writable only as the unit's RuntimeDirectory.
	"RuntimeDirectory": true, "RuntimeDirectoryMode": true, "RuntimeDirectoryPreserve": true,
}

// hostTreeRoots are never made writable for the harness's vmmd.
var hostTreeRoots = []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/proc", "/sys", "/dev", "/etc"}

// vmmdSystemdArgv wraps binary in a transient service that carries the
// production unit's sandbox. The harness's own directories (sockets, config,
// journal, storage, scratch) are added to ReadWritePaths: production keeps
// them under paths the unit already lists.
func vmmdSystemdArgv(t *testing.T, binary string, args, env []string) []string {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatalf("e2etest: %s=1 needs root to start a system service", vmmdSystemdEnv)
	}
	runner, err := exec.LookPath("systemd-run")
	if err != nil {
		t.Fatalf("e2etest: %s=1 needs systemd-run: %v", vmmdSystemdEnv, err)
	}
	unit, err := daemonunitspec.UnitByName("vmmd")
	if err != nil {
		t.Fatalf("e2etest: look up the vmmd unit spec: %v", err)
	}
	var id [4]byte
	_, _ = rand.Read(id[:])
	name := vmmdUnitPrefix + hex.EncodeToString(id[:]) + ".service"
	argv := []string{runner, "--wait", "--pipe", "--quiet", "--collect", "--service-type=exec", "--unit=" + name}
	for _, prop := range vmmdSandboxProperties(unit.Render(), harnessWritablePaths(env)) {
		argv = append(argv, "--property="+prop)
	}
	// Later entries win, as in exec's own environment.
	merged := map[string]string{}
	var order []string
	for _, kv := range append(os.Environ(), env...) {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" || strings.ContainsAny(v, "\n\x00") {
			continue
		}
		if _, seen := merged[k]; !seen {
			order = append(order, k)
		}
		merged[k] = v
	}
	for _, k := range order {
		argv = append(argv, "--setenv="+k+"="+merged[k])
	}
	argv = append(argv, "--", binary)
	argv = append(argv, args...)
	t.Cleanup(func() {
		_ = exec.Command("systemctl", "stop", name).Run()
		_ = exec.Command("systemctl", "reset-failed", name).Run()
	})
	return argv
}

// vmmdSandboxProperties picks the sandbox settings out of the rendered
// production unit's [Service] section, adding writable to ReadWritePaths and
// marking every ReadWritePaths entry optional.
func vmmdSandboxProperties(rendered []byte, writable []string) []string {
	var props []string
	sc := bufio.NewScanner(bytes.NewReader(rendered))
	inService := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inService = line == "[Service]"
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !inService || !ok || !vmmdSandboxKeys[key] {
			continue
		}
		if key == "ReadWritePaths" {
			// A production path the test host lacks (/var/lib/faas/cache)
			// would fail the start with 226/NAMESPACE; the sandbox is the
			// same wherever the path exists.
			var paths []string
			for _, path := range strings.Fields(value) {
				if !strings.HasPrefix(path, "-") {
					path = "-" + path
				}
				paths = append(paths, path)
			}
			value = strings.Join(append(paths, writable...), " ")
		}
		props = append(props, key+"="+value)
	}
	return props
}

// harnessWritablePaths are the directories the harness hands vmmd in its
// environment, marked optional ("-") so a path removed meanwhile does not
// fail the start.
func harnessWritablePaths(env []string) []string {
	var out []string
	add := func(dir string) {
		dir = filepath.Clean(dir)
		if dir == "/" || slices.ContainsFunc(hostTreeRoots, func(root string) bool {
			return dir == root || strings.HasPrefix(dir, root+"/")
		}) {
			return
		}
		if !slices.Contains(out, "-"+dir) {
			out = append(out, "-"+dir)
		}
	}
	add(os.TempDir())
	for _, kv := range env {
		_, v, ok := strings.Cut(kv, "=")
		// vmmd's listen address is a unix:// URL; its socket directory must
		// be writable, or the sandbox's read-only /tmp refuses the bind.
		v = strings.TrimPrefix(v, "unix://")
		if !ok || !filepath.IsAbs(v) || strings.ContainsAny(v, " :") {
			continue
		}
		if info, err := os.Stat(v); err == nil && info.IsDir() {
			add(v)
		} else if parent := filepath.Dir(v); parent != v {
			if info, err := os.Stat(parent); err == nil && info.IsDir() {
				add(parent)
			}
		}
	}
	return out
}

// vmmdUnitOf names the transient service a systemd-run wrapper started, or "".
func vmmdUnitOf(p *exec.Cmd) string {
	if p == nil {
		return ""
	}
	for _, arg := range p.Args {
		if unit, ok := strings.CutPrefix(arg, "--unit="); ok && strings.HasPrefix(unit, vmmdUnitPrefix) {
			return unit
		}
		if arg == "--" {
			break
		}
	}
	return ""
}

// isVMMDProc reports whether p is the harness's vmmd, run directly or as a
// transient service.
func isVMMDProc(p *exec.Cmd) bool {
	return p != nil && (filepath.Base(p.Path) == "vmmd" || vmmdUnitOf(p) != "")
}

// stopVMMD asks vmmd to stop as its service manager does on a release
// (SIGTERM to vmmd). A transient service is stopped through systemd; its
// wrapper exits once the service has.
func stopVMMD(p *exec.Cmd) error {
	if unit := vmmdUnitOf(p); unit != "" {
		return exec.Command("systemctl", "stop", "--no-block", unit).Run()
	}
	return p.Process.Signal(syscall.SIGTERM)
}

// killVMMD ends vmmd at once, as a crash does.
func killVMMD(p *exec.Cmd) error {
	if unit := vmmdUnitOf(p); unit != "" {
		return exec.Command("systemctl", "kill", "--signal=SIGKILL", "--kill-whom=main", unit).Run()
	}
	return p.Process.Kill()
}
