//go:build linux

package objectqualification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func inspectionFixture(request inspectionRequest, boot string) HostInspection {
	storage := eligibleInspectionStorage()
	storage.Parent = filepath.Dir(request.Directory)
	return HostInspection{Version: 1, Role: request.Role, Directory: request.Directory, Platform: "linux/amd64", KernelBootSHA256: strings.Repeat(boot, 64), DaemonSHA256: strings.Repeat("c", 64), ControlSHA256: strings.Repeat("d", 64), AgentSHA256: strings.Repeat("e", 64), Storage: storage}
}

func TestPreflightHostAndBinarySeparation(t *testing.T) {
	primaryRequest := inspectionRequest{Directory: "/mnt/objects/primary", Daemon: "/daemon", Control: "/ctl", Role: "primary"}
	followerRequest := inspectionRequest{Directory: "/mnt/objects/follower", Daemon: "/daemon", Control: "/ctl", Role: "follower"}
	for _, tc := range []struct {
		name   string
		change func(*HostInspection)
		bad    bool
	}{
		{name: "distinct kernels and matching binaries"},
		{name: "same kernel", bad: true, change: func(h *HostInspection) { h.KernelBootSHA256 = strings.Repeat("a", 64) }},
		{name: "different daemon", bad: true, change: func(h *HostInspection) { h.DaemonSHA256 = strings.Repeat("f", 64) }},
		{name: "different control", bad: true, change: func(h *HostInspection) { h.ControlSHA256 = strings.Repeat("f", 64) }},
		{name: "different helper", bad: true, change: func(h *HostInspection) { h.AgentSHA256 = strings.Repeat("f", 64) }},
		{name: "different platform", bad: true, change: func(h *HostInspection) { h.Platform = "linux/arm64" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary, follower := inspectionFixture(primaryRequest, "a"), inspectionFixture(followerRequest, "b")
			if tc.change != nil {
				tc.change(&follower)
			}
			report := PreflightReport{Primary: &primary, Follower: &follower}
			compareInspectionHosts(&report, true)
			bad := false
			for _, check := range report.Checks {
				bad = bad || !check.Passed
			}
			if bad != tc.bad || report.PhysicalFailureDomainsVerified {
				t.Fatal("incorrect separation claim", report)
			}
		})
	}
	valid := inspectionFixture(followerRequest, "b")
	for _, change := range []func(*HostInspection){func(h *HostInspection) { h.Directory = "/another/path" }, func(h *HostInspection) { h.Role = "primary" }, func(h *HostInspection) { h.Version = 0 }, func(h *HostInspection) { h.Storage.Parent = "/another" }, func(h *HostInspection) { h.AgentSHA256 = "missing" }} {
		bad := valid
		change(&bad)
		if err := validateHostInspection(bad, followerRequest); err == nil {
			t.Fatal("foreign or incomplete inspection accepted", bad)
		}
	}
}

func TestPreflightMissingRemotePreservesFailureReportWithoutData(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "planned")
	report, err := Preflight(t.Context(), PreflightOptions{Daemon: "/bin/true", Control: "/bin/true", Directory: directory})
	if err == nil || report.Ready || report.Primary == nil || report.Follower != nil || report.PhysicalFailureDomainsVerified {
		t.Fatal(report, err)
	}
	if _, err = os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed preflight created a fixture", err)
	}
	path := filepath.Join(root, "evidence.json")
	if err = WritePreflightReport(path, report); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = WritePreflightReport(path, PreflightReport{Ready: true}); err == nil {
		t.Fatal("failure evidence was replaced")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failure report changed")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatal("report not private", info.Mode())
	}
}

func TestPreflightSSHInspectionUsesPrivateInputAndBoundedOutput(t *testing.T) {
	root := t.TempDir()
	for key, value := range map[string]string{"FAAS_INSPECTION_ARGS": filepath.Join(root, "args"), "FAAS_INSPECTION_REQUEST": filepath.Join(root, "request"), "FAAS_INSPECTION_REPLY": filepath.Join(root, "reply"), "FAAS_OBJECT_NODE_ACCESS_KEY_ID": "synthetic", "FAAS_OBJECT_NODE_SECRET_ACCESS_KEY": "synthetic"} {
		t.Setenv(key, value)
	}
	script := `#!/bin/sh
if [ "${FAAS_OBJECT_NODE_ACCESS_KEY_ID+set}" = set ] || [ "${FAAS_OBJECT_NODE_SECRET_ACCESS_KEY+set}" = set ]; then exit 71; fi
printf '%s\n' "$@" > "$FAAS_INSPECTION_ARGS"
cat > "$FAAS_INSPECTION_REQUEST"
cat "$FAAS_INSPECTION_REPLY"
printf '%s\n' 'private transport diagnostic' >&2
exit "${FAAS_INSPECTION_EXIT:-0}"
`
	if err := os.WriteFile(filepath.Join(root, "ssh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+":"+os.Getenv("PATH"))
	request := inspectionRequest{Directory: "/mnt/objects/next private", Daemon: "/opt/node daemon", Control: "/opt/node ctl", Role: "follower"}
	o := RemoteOptions{Host: "qualification-follower", Agent: "/opt/helper's name", Daemon: request.Daemon, Control: request.Control, Directory: request.Directory}
	body, _ := json.Marshal(inspectionFixture(request, "a"))
	if err := os.WriteFile(os.Getenv("FAAS_INSPECTION_REPLY"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectRemote(t.Context(), o, request); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(os.Getenv("FAAS_INSPECTION_ARGS"))
	for _, option := range []string{"-oBatchMode=yes", "-oStrictHostKeyChecking=yes", "-oClearAllForwardings=yes", "-oPermitLocalCommand=no", "-oControlMaster=no"} {
		if !strings.Contains(string(args), option) {
			t.Fatal("missing SSH protection", option, string(args))
		}
	}
	if strings.Contains(string(args), request.Directory) || !strings.HasSuffix(string(args), "exec "+shellArgument(o.Agent)+" --inspect\n") {
		t.Fatal("paths leaked to argv or executable unquoted", string(args))
	}
	input, _ := os.ReadFile(os.Getenv("FAAS_INSPECTION_REQUEST"))
	var captured inspectionRequest
	if json.Unmarshal(input, &captured) != nil || captured != request {
		t.Fatal("inspection was not sent on stdin", string(input))
	}
	t.Setenv("FAAS_INSPECTION_EXIT", "9")
	if _, err := inspectRemote(t.Context(), o, request); err == nil || strings.Contains(err.Error(), "private transport diagnostic") {
		t.Fatal("SSH error or private stderr exposed", err)
	}
}

func TestPreflightSSHInspectionCancellationReapsChild(t *testing.T) {
	root := t.TempDir()
	pidFile := filepath.Join(root, "pid")
	t.Setenv("FAAS_INSPECTION_PID", pidFile)
	if err := os.WriteFile(filepath.Join(root, "ssh"), []byte("#!/bin/sh\nprintf '%s' \"$$\" > \"$FAAS_INSPECTION_PID\"\nexec /bin/sleep 60\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+":"+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	o := RemoteOptions{Host: "follower", Agent: "/agent", Daemon: "/daemon", Control: "/ctl", Directory: "/mnt/objects/run"}
	go func() {
		_, err := inspectRemote(ctx, o, inspectionRequest{Directory: o.Directory, Daemon: o.Daemon, Control: o.Control, Role: "follower"})
		done <- err
	}()
	deadline, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	if err := poll(deadline, func() (bool, error) { _, err := os.Stat(pidFile); return err == nil, nil }); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(pidFile)
	var pid int
	if _, err := fmt.Sscan(string(data), &pid); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-deadline.Done():
		t.Fatal("SSH inspection did not stop")
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatal("SSH inspection child survived", pid, err)
	}
}
