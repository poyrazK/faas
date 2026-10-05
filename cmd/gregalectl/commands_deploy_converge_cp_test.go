package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// adr: 580

func writeContractTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for path, body := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestControlPlaneBootstrapContractHashTracksInputs(t *testing.T) {
	base := map[string]string{
		"bootstrap.yml":                        "---\n- hosts: control_plane:compute_nodes\n  tasks: []\n- hosts: control_plane\n  roles:\n    - role: control\n- hosts: compute_nodes\n  roles:\n    - role: compute\n",
		"control_plane_converge.yml":           "---\n",
		"ansible.cfg":                          "[defaults]\n",
		"requirements.yml":                     "collections: []\n",
		"roles/_shared/tasks/assert.yml":       "---\n",
		"roles/control/tasks/main.yml":         "---\n",
		"roles/compute/tasks/main.yml":         "---\n",
		"tasks/validate_udp_policy.yml":        "---\n",
		"group_vars/control_plane/backup.yml":  "a: 1\n",
		"group_vars/compute_nodes/log.yml":     "b: 1\n",
		"inventory-source/host_vars/fsn-1.yml": "ansible_host: fsn-1\n",
		"inventory-source/hosts.ini":           "[control_plane]\nfsn-1\n",
		"operator/prod-vars.yml":               "postgres_password: one\n",
	}
	hashFor := func(t *testing.T, change map[string]string, inventoryRoot string) string {
		t.Helper()
		dir := t.TempDir()
		files := map[string]string{}
		for k, v := range base {
			files[k] = v
		}
		for k, v := range change {
			files[k] = v
		}
		writeContractTree(t, dir, files)
		if inventoryRoot == "" {
			inventoryRoot = filepath.Join(dir, "rendered")
		}
		inventory := []manifestAnsibleFile{
			{Path: filepath.Join(inventoryRoot, "inventory", "hosts.ini"), Body: []byte(files["inventory-source/hosts.ini"])},
			{Path: filepath.Join(inventoryRoot, "inventory", "host_vars", "fsn-1.yml"), Body: []byte(files["inventory-source/host_vars/fsn-1.yml"])},
		}
		got, err := controlPlaneBootstrapContractHash(dir, inventoryRoot, inventory, filepath.Join(dir, "operator/prod-vars.yml"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(got, "sha256:") || len(got) != len("sha256:")+64 {
			t.Fatalf("contract hash = %q", got)
		}
		return got
	}
	baseline := hashFor(t, nil, "")
	if again := hashFor(t, nil, filepath.Join(t.TempDir(), "elsewhere")); again != baseline {
		t.Fatalf("contract depends on the temporary inventory location: %s != %s", again, baseline)
	}

	tests := []struct {
		name    string
		change  map[string]string
		changes bool
	}{
		{name: "control-plane role", change: map[string]string{"roles/control/tasks/main.yml": "- debug: {}\n"}, changes: true},
		{name: "shared task library", change: map[string]string{"roles/_shared/tasks/assert.yml": "- debug: {}\n"}, changes: true},
		{name: "role_path task include", change: map[string]string{"tasks/validate_udp_policy.yml": "- debug: {}\n"}, changes: true},
		{name: "control-plane group_vars", change: map[string]string{"group_vars/control_plane/backup.yml": "a: 2\n"}, changes: true},
		{name: "ansible.cfg", change: map[string]string{"ansible.cfg": "[defaults]\nforks = 2\n"}, changes: true},
		{name: "collection pins", change: map[string]string{"requirements.yml": "collections: [x]\n"}, changes: true},
		{name: "converge playbook", change: map[string]string{"control_plane_converge.yml": "---\n# changed\n"}, changes: true},
		{name: "rendered inventory", change: map[string]string{"inventory-source/host_vars/fsn-1.yml": "ansible_host: fsn-9\n"}, changes: true},
		{name: "operator vars", change: map[string]string{"operator/prod-vars.yml": "postgres_password: two\n"}, changes: true},
		{name: "compute-only role", change: map[string]string{"roles/compute/tasks/main.yml": "- debug: {}\n"}, changes: false},
		{name: "compute group_vars", change: map[string]string{"group_vars/compute_nodes/log.yml": "b: 2\n"}, changes: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hashFor(t, tt.change, "")
			if changed := got != baseline; changed != tt.changes {
				t.Fatalf("contract changed=%v, want %v", changed, tt.changes)
			}
		})
	}
}

func TestDeployConvergeControlPlaneValidate(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "file")
	if err := os.WriteFile(existing, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	valid := deployConvergeControlPlaneOptions{ManifestFile: existing, SSHKnownHostsFile: existing, AnsibleVarsFile: existing, SSHUser: "root"}
	tests := []struct {
		name   string
		mutate func(*deployConvergeControlPlaneOptions)
		want   string
	}{
		{name: "valid", mutate: func(*deployConvergeControlPlaneOptions) {}},
		{name: "no manifest", mutate: func(o *deployConvergeControlPlaneOptions) { o.ManifestFile = "" }, want: "--manifest-file is required"},
		{name: "no known hosts", mutate: func(o *deployConvergeControlPlaneOptions) { o.SSHKnownHostsFile = "" }, want: "--ssh-known-hosts-file is required"},
		{name: "no vars", mutate: func(o *deployConvergeControlPlaneOptions) { o.AnsibleVarsFile = "" }, want: "--ansible-vars-file is required"},
		{name: "missing vars file", mutate: func(o *deployConvergeControlPlaneOptions) { o.AnsibleVarsFile = filepath.Join(dir, "missing") }, want: "--ansible-vars-file"},
		{name: "missing key", mutate: func(o *deployConvergeControlPlaneOptions) { o.SSHKey = filepath.Join(dir, "missing") }, want: "--ssh-key"},
		{name: "empty user", mutate: func(o *deployConvergeControlPlaneOptions) { o.SSHUser = "" }, want: "--ssh-user"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := valid
			tt.mutate(&opts)
			err := opts.validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validate() = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestConvergeControlPlaneRunsLimitedVerifiedPlaybook(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBJcCV3B7r6Ey6qjXgmPLQxZQ6Ho9dJv0h5vPXLqyYV3"
	if err := os.WriteFile(knownHosts, []byte("fsn-1.gregale.dev "+key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	varsFile := filepath.Join(dir, "prod-vars.yml")
	if err := os.WriteFile(varsFile, []byte("am_dev_mode: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldLookup, oldRunner := joinPrivateAddressLookup, ansiblePlaybookRunner
	t.Cleanup(func() { joinPrivateAddressLookup, ansiblePlaybookRunner = oldLookup, oldRunner })
	joinPrivateAddressLookup = func(_ context.Context, _, host string) ([]net.IP, error) {
		switch host {
		case "fsn-1.gregale.dev":
			return []net.IP{net.ParseIP("10.42.0.1")}, nil
		case "fsn-2.gregale.dev":
			return []net.IP{net.ParseIP("10.42.0.2")}, nil
		}
		return nil, fmt.Errorf("unexpected private host %s", host)
	}
	var gotDir string
	var gotArgs []string
	var convergeVars map[string]any
	var computeHostVars string
	ansiblePlaybookRunner = func(_ context.Context, workingDir string, args []string) error {
		gotDir, gotArgs = workingDir, append([]string(nil), args...)
		for i := range args {
			if args[i] == "-e" && strings.HasSuffix(args[i+1], "converge-vars.json") {
				body, err := os.ReadFile(strings.TrimPrefix(args[i+1], "@"))
				if err != nil {
					return err
				}
				if err := json.Unmarshal(body, &convergeVars); err != nil {
					return err
				}
			}
			if args[i] == "-i" {
				body, err := os.ReadFile(filepath.Join(filepath.Dir(args[i+1]), "host_vars", "fsn-2.yml"))
				if err != nil {
					return err
				}
				computeHostVars = string(body)
			}
		}
		return nil
	}

	report, err := convergeControlPlane(context.Background(), deployConvergeControlPlaneOptions{
		ManifestFile:      filepath.Join(repoRoot, "deploy", "manifest", "examples", "splitbox.example.yaml"),
		SSHKnownHostsFile: knownHosts,
		SSHUser:           "root",
		AnsibleVarsFile:   varsFile,
		RepoRoot:          repoRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Host != "fsn-1" || !strings.HasPrefix(report.Contract, "sha256:") {
		t.Fatalf("report = %+v", report)
	}
	if gotDir != filepath.Join(repoRoot, "deploy", "ansible") {
		t.Fatalf("ansible-playbook ran in %q; ansible.cfg (force_handlers, host key checking) lives in deploy/ansible", gotDir)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{
		"--limit control_plane",
		"-o UserKnownHostsFile=" + knownHosts + " -o StrictHostKeyChecking=yes",
		"--user root",
		"-e @" + varsFile,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("ansible-playbook args missing %q:\n%s", want, joined)
		}
	}
	if last := gotArgs[len(gotArgs)-1]; last != filepath.Join(repoRoot, "deploy", "ansible", controlPlaneConvergePlaybook) {
		t.Errorf("playbook = %q, want %s", last, controlPlaneConvergePlaybook)
	}
	if convergeVars["faas_cp_bootstrap_contract_sha256"] != report.Contract {
		t.Errorf("converge vars contract = %v, want %s", convergeVars["faas_cp_bootstrap_contract_sha256"], report.Contract)
	}
	if convergeVars["faas_join_defer_service_handlers"] != true {
		t.Errorf("converge must defer FaaS daemon restarts to release activation; vars = %v", convergeVars)
	}
	if !strings.Contains(computeHostVars, `faas_private_address: "10.42.0.2"`) {
		t.Errorf("compute peer private address was not seeded from DNS:\n%s", computeHostVars)
	}
}

func TestConvergeControlPlaneRefusesUnverifiedHostKey(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHosts, []byte("other.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBJcCV3B7r6Ey6qjXgmPLQxZQ6Ho9dJv0h5vPXLqyYV3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	varsFile := filepath.Join(dir, "prod-vars.yml")
	if err := os.WriteFile(varsFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldRunner := ansiblePlaybookRunner
	t.Cleanup(func() { ansiblePlaybookRunner = oldRunner })
	ansiblePlaybookRunner = func(context.Context, string, []string) error {
		t.Fatal("ansible-playbook must not run without a verified control-plane host key")
		return nil
	}
	_, err = convergeControlPlane(context.Background(), deployConvergeControlPlaneOptions{
		ManifestFile:      filepath.Join(repoRoot, "deploy", "manifest", "examples", "splitbox.example.yaml"),
		SSHKnownHostsFile: knownHosts,
		SSHUser:           "root",
		AnsibleVarsFile:   varsFile,
		RepoRoot:          repoRoot,
	})
	if err == nil || !strings.Contains(err.Error(), "fsn-1") {
		t.Fatalf("convergeControlPlane() = %v, want a known_hosts error naming fsn-1", err)
	}
}

func TestControlPlaneConvergePlaybookRecordsContractOnlyAfterBootstrap(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", controlPlaneConvergePlaybook))
	if err != nil {
		t.Fatal(err)
	}
	playbook := string(body)
	probe := strings.Index(playbook, "control-plane-bootstrap-contract.sha256")
	fastPath := strings.Index(playbook, "faas_join_bootstrap_contract_current:")
	imported := strings.Index(playbook, "ansible.builtin.import_playbook: bootstrap.yml")
	persist := strings.Index(playbook, "- name: Persist the converged control-plane bootstrap contract")
	if probe < 0 || fastPath < 0 || imported < 0 || persist < 0 {
		t.Fatalf("converge playbook must probe the contract, select the fast path, import bootstrap.yml and persist the contract")
	}
	if !(probe < imported && fastPath < imported && imported < persist) {
		t.Fatalf("contract must be probed before bootstrap.yml and recorded only after it")
	}
	bootstrap, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "bootstrap.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bootstrap), "when: faas_join_bootstrap_contract_current | default(false) | bool") {
		t.Fatal("bootstrap.yml no longer honors faas_join_bootstrap_contract_current; an unchanged control-plane contract would reconverge every rollout")
	}
}

// convergeFixture returns valid options against the example split-box
// manifest with DNS and Ansible seams installed; tests override pieces.
func convergeFixture(t *testing.T) deployConvergeControlPlaneOptions {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHosts, []byte("fsn-1.gregale.dev ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBJcCV3B7r6Ey6qjXgmPLQxZQ6Ho9dJv0h5vPXLqyYV3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	varsFile := filepath.Join(dir, "prod-vars.yml")
	if err := os.WriteFile(varsFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldLookup, oldRunner := joinPrivateAddressLookup, ansiblePlaybookRunner
	t.Cleanup(func() { joinPrivateAddressLookup, ansiblePlaybookRunner = oldLookup, oldRunner })
	joinPrivateAddressLookup = func(_ context.Context, _, host string) ([]net.IP, error) {
		switch host {
		case "fsn-1.gregale.dev":
			return []net.IP{net.ParseIP("10.42.0.1")}, nil
		case "fsn-2.gregale.dev":
			return []net.IP{net.ParseIP("10.42.0.2")}, nil
		}
		return nil, fmt.Errorf("unexpected private host %s", host)
	}
	ansiblePlaybookRunner = func(context.Context, string, []string) error { return nil }
	return deployConvergeControlPlaneOptions{
		ManifestFile:      filepath.Join(repoRoot, "deploy", "manifest", "examples", "splitbox.example.yaml"),
		SSHKnownHostsFile: knownHosts,
		SSHUser:           "root",
		AnsibleVarsFile:   varsFile,
		RepoRoot:          repoRoot,
	}
}

func TestCmdDeployConvergeControlPlaneExitCodesAndOutput(t *testing.T) {
	tests := []struct {
		name       string
		args       func(o deployConvergeControlPlaneOptions) []string
		runnerErr  error
		wantCode   int
		wantStdout string
	}{
		{
			name:     "missing required flags",
			args:     func(deployConvergeControlPlaneOptions) []string { return nil },
			wantCode: 2,
		},
		{
			name:     "unknown flag",
			args:     func(deployConvergeControlPlaneOptions) []string { return []string{"--no-such-flag"} },
			wantCode: 2,
		},
		{
			name: "text report",
			args: func(o deployConvergeControlPlaneOptions) []string {
				return []string{"--manifest-file", o.ManifestFile, "--ssh-known-hosts-file", o.SSHKnownHostsFile, "--ansible-vars-file", o.AnsibleVarsFile, "--repo-root", o.RepoRoot}
			},
			wantCode:   0,
			wantStdout: "deploy converge-control-plane: host=fsn-1 contract=sha256:",
		},
		{
			name: "json report",
			args: func(o deployConvergeControlPlaneOptions) []string {
				return []string{"--manifest-file", o.ManifestFile, "--ssh-known-hosts-file", o.SSHKnownHostsFile, "--ansible-vars-file", o.AnsibleVarsFile, "--repo-root", o.RepoRoot, "--json"}
			},
			wantCode:   0,
			wantStdout: `"contract_sha256":"sha256:`,
		},
		{
			name: "ansible failure",
			args: func(o deployConvergeControlPlaneOptions) []string {
				return []string{"--manifest-file", o.ManifestFile, "--ssh-known-hosts-file", o.SSHKnownHostsFile, "--ansible-vars-file", o.AnsibleVarsFile, "--repo-root", o.RepoRoot}
			},
			runnerErr: errors.New("play failed"),
			wantCode:  3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := convergeFixture(t)
			ansiblePlaybookRunner = func(context.Context, string, []string) error { return tt.runnerErr }
			var code int
			buf, _ := captureStdoutComputeNodes(t, func() {
				_ = captureStderrComputeNodes(t, func() { code = cmdDeployDispatch(append([]string{"converge-control-plane"}, tt.args(opts)...)) })
			})
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d (stdout %q)", code, tt.wantCode, buf.String())
			}
			if tt.wantStdout != "" && !strings.Contains(strings.ReplaceAll(buf.String(), " ", ""), strings.ReplaceAll(tt.wantStdout, " ", "")) {
				t.Fatalf("stdout = %q, want it to contain %q", buf.String(), tt.wantStdout)
			}
		})
	}
}

func TestConvergeControlPlaneFailurePaths(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, o *deployConvergeControlPlaneOptions)
		want   string
	}{
		{
			name: "release predates the converge playbook",
			mutate: func(t *testing.T, o *deployConvergeControlPlaneOptions) {
				o.RepoRoot = t.TempDir()
			},
			want: "control_plane_converge.yml",
		},
		{
			name: "unreadable manifest",
			mutate: func(t *testing.T, o *deployConvergeControlPlaneOptions) {
				o.ManifestFile = filepath.Join(t.TempDir(), "missing.yaml")
			},
			want: "missing.yaml",
		},
		{
			name: "manifest without a control plane",
			mutate: func(t *testing.T, o *deployConvergeControlPlaneOptions) {
				body, err := os.ReadFile(o.ManifestFile)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "no-cp.yaml")
				rewritten := strings.Replace(string(body), "role: control-plane", "role: compute-only", 1)
				if err := os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
					t.Fatal(err)
				}
				o.ManifestFile = path
			},
			want: "control-plane",
		},
		{
			name: "peer private name does not resolve",
			mutate: func(t *testing.T, o *deployConvergeControlPlaneOptions) {
				joinPrivateAddressLookup = func(context.Context, string, string) ([]net.IP, error) {
					return nil, errors.New("no such host")
				}
			},
			want: "resolve fleet private addresses",
		},
		{
			name: "ansible play fails",
			mutate: func(t *testing.T, o *deployConvergeControlPlaneOptions) {
				ansiblePlaybookRunner = func(context.Context, string, []string) error { return errors.New("exit status 2") }
			},
			want: "control-plane convergence: exit status 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := convergeFixture(t)
			tt.mutate(t, &opts)
			_, err := convergeControlPlane(context.Background(), opts)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("convergeControlPlane() = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestSoleControlPlaneHostRequiresExactlyOne(t *testing.T) {
	tests := []struct {
		name  string
		roles []string
		want  string
	}{
		{name: "one", roles: []string{roleControlPlane, roleComputeOnly}, want: "cp-0"},
		{name: "none", roles: []string{roleComputeOnly}},
		{name: "two", roles: []string{roleControlPlane, roleControlPlane}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &manifest.Manifest{}
			for i, role := range tt.roles {
				m.Fleet.Hosts = append(m.Fleet.Hosts, manifest.Host{Name: fmt.Sprintf("cp-%d", i), Role: role})
			}
			got, err := soleControlPlaneHost(m)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("soleControlPlaneHost() = %+v, want an error", got)
				}
				return
			}
			if err != nil || got.Name != tt.want {
				t.Fatalf("soleControlPlaneHost() = %+v, %v; want %s", got, err, tt.want)
			}
		})
	}
}

func TestControlPlaneBootstrapContractHashRequiresAControlPlanePlay(t *testing.T) {
	dir := t.TempDir()
	writeContractTree(t, dir, map[string]string{
		"bootstrap.yml":    "---\n- hosts: compute_nodes\n  roles:\n    - role: compute\n",
		"prod-vars.yml":    "{}\n",
		"requirements.yml": "collections: []\n",
	})
	if _, err := controlPlaneBootstrapContractHash(dir, dir, nil, filepath.Join(dir, "prod-vars.yml")); err == nil || !strings.Contains(err.Error(), "no control_plane play") {
		t.Fatalf("contract without a control_plane play = %v, want an error", err)
	}
	if _, err := controlPlaneBootstrapContractHash(t.TempDir(), dir, nil, filepath.Join(dir, "prod-vars.yml")); err == nil {
		t.Fatal("contract without bootstrap.yml must fail")
	}
}
