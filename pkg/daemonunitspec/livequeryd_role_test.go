package daemonunitspec

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// livequerydFlags returns the flag names cmd/livequeryd defines.
func livequerydFlags(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "cmd", "livequeryd", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	def := regexp.MustCompile(`flag\.(?:String|Bool|Duration|Int)\("([a-z-]+)"`)
	flags := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range def.FindAllSubmatch(src, -1) {
			flags[string(m[1])] = true
		}
	}
	return flags
}

// renderLivequerydTemplate renders one of the role's templates with the
// role's defaults overridden by vars, through ansible's own templating.
func renderLivequerydTemplate(t *testing.T, name string, vars map[string]any) string {
	t.Helper()
	role := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "livequeryd_service")
	raw, err := os.ReadFile(filepath.Join(role, "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]any{}
	if err := yaml.Unmarshal(raw, &all); err != nil {
		t.Fatal(err)
	}
	for k, v := range vars {
		all[k] = v
	}
	tmpl, err := os.ReadFile(filepath.Join(role, "templates", name))
	if err != nil {
		t.Fatal(err)
	}
	return renderPublicEdgeBlock(t, string(tmpl), all)
}

// livequerydDatabases is two databases: one with the role-wide defaults, one
// releasing to zero on Neon with its own API key.
var livequerydDatabases = []map[string]any{
	{"id": "3f1c", "database_url_src": "secrets/3f1c.dsn"},
	{"id": "9a2b", "database_url_src": "secrets/9a2b.dsn", "slot": "live_b", "release_after": "5m", "awake": "neon",
		"neon_project": "proj-1", "neon_endpoint": "ep-1", "neon_api_key_src": "secrets/9a2b.neon"},
}

// livequerydDropIn is a rendered hub drop-in: its credential names and its
// one command line.
type livequerydDropIn struct {
	text  string
	creds []string
	exec  string
}

func renderLivequerydDropIn(t *testing.T, vars map[string]any) livequerydDropIn {
	t.Helper()
	d := livequerydDropIn{text: renderLivequerydTemplate(t, "50-faas-livequeryd.conf.j2", vars)}
	lines := strings.Split(d.text, "\n")
	var execs []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if after, ok := strings.CutPrefix(line, "LoadCredential="); ok {
			d.creds = append(d.creds, strings.SplitN(after, ":", 2)[0])
		}
		if !strings.HasPrefix(line, "ExecStart=/") {
			continue
		}
		// systemd joins a line ending in a backslash with the next; a blank
		// line in between would end the command early.
		cmd := line
		for strings.HasSuffix(cmd, `\`) {
			i++
			if i == len(lines) || strings.TrimSpace(lines[i]) == "" {
				t.Fatalf("ExecStart continuation ends at a blank line:\n%s", d.text)
			}
			cmd = strings.TrimSuffix(cmd, `\`) + " " + strings.TrimSpace(lines[i])
		}
		execs = append(execs, cmd)
	}
	if len(execs) != 1 || !strings.Contains(d.text, "ExecStart=\n") {
		t.Fatalf("want ExecStart= reset then one command line, got %d:\n%s", len(execs), d.text)
	}
	d.exec = execs[0]
	return d
}

// adr: 20261016151947 — the role's drop-in is the hub's whole command line:
// one ExecStart whose flags livequeryd defines, serving the list of
// databases, with every secret passed as a systemd credential under %d
// rather than as a path the faas user could read or a value on the command
// line.
func TestLivequerydDropInIsOneCommandLine(t *testing.T) {
	flags := livequerydFlags(t)
	for name, tc := range map[string]struct {
		vars  map[string]any
		creds []string
		want  []string
	}{
		"tls, two databases": {
			vars:  map[string]any{"faas_livequeryd_databases": livequerydDatabases},
			creds: []string{"keys_dsn", "keys_identity", "database_url.3f1c", "database_url.9a2b", "neon_api_key.9a2b", "tls_key"},
			want:  []string{"-tls-key=%d/tls_key", "-client-ca=/etc/faas/tls/ca/ca.crt", "-client-name=realtimed.faas", "-listen=0.0.0.0:9480"},
		},
		"shared neon key, loopback": {
			vars: map[string]any{
				"faas_livequeryd_databases": livequerydDatabases[:1], "faas_livequeryd_tls_cert": "", "faas_livequeryd_listen": "127.0.0.1:9480",
				"faas_livequeryd_neon_api_key_src": "secrets/neon", "faas_livequeryd_memory_max": "2G",
			},
			creds: []string{"keys_dsn", "keys_identity", "database_url.3f1c", "neon_api_key"},
			want:  []string{"-neon-api-key-file=%d/neon_api_key", "-listen=127.0.0.1:9480"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := renderLivequerydDropIn(t, tc.vars)
			if strings.Join(d.creds, ",") != strings.Join(tc.creds, ",") {
				t.Errorf("credentials = %v, want %v", d.creds, tc.creds)
			}
			memory := "512M"
			if m, ok := tc.vars["faas_livequeryd_memory_max"]; ok {
				memory = m.(string)
			}
			if !strings.Contains(d.text, "\nMemoryMax="+memory+"\n") {
				t.Errorf("drop-in lacks MemoryMax=%s:\n%s", memory, d.text)
			}
			args := strings.Fields(strings.TrimPrefix(d.exec, "ExecStart="))
			if args[0] != "/opt/faas/current/bin/livequeryd" {
				t.Fatalf("ExecStart runs %q", args[0])
			}
			for _, arg := range args[1:] {
				name, _, _ := strings.Cut(strings.TrimPrefix(arg, "-"), "=")
				if !strings.HasPrefix(arg, "-") || !flags[name] {
					t.Errorf("ExecStart passes %q, which livequeryd does not define", arg)
				}
				if strings.Contains(arg, "/etc/faas/secrets") || strings.Contains(arg, ".key") {
					t.Errorf("ExecStart names a secret file directly: %q", arg)
				}
			}
			for _, w := range append(tc.want, "-databases-file=/etc/faas/livequeryd/databases.json", "-checkpoint-dir=/var/lib/faas/livequeryd",
				"-keys-dsn-file=%d/keys_dsn", "-keys-identity=%d/keys_identity") {
				if !strings.Contains(d.exec, " "+w) {
					t.Errorf("ExecStart lacks %s:\n%s", w, d.exec)
				}
			}
		})
	}
}

// adr: 20261016151947 — the role's list of databases is a -databases-file
// livequeryd reads: only fields it defines, each secret a credential the
// drop-in loads, and the role-wide defaults for what an entry leaves out.
func TestLivequerydDatabasesFile(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "cmd", "livequeryd", "databases.go"))
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]bool{}
	for _, m := range regexp.MustCompile(`json:"([a-z_]+)`).FindAllSubmatch(src, -1) {
		fields[string(m[1])] = true
	}
	vars := map[string]any{"faas_livequeryd_databases": livequerydDatabases}
	var file struct {
		Databases []map[string]any `json:"databases"`
	}
	text := renderLivequerydTemplate(t, "databases.json.j2", vars)
	if err := json.Unmarshal([]byte(text), &file); err != nil {
		t.Fatalf("databases.json is not JSON: %v\n%s", err, text)
	}
	creds := map[string]bool{}
	for _, c := range renderLivequerydDropIn(t, vars).creds {
		creds[c] = true
	}
	if len(file.Databases) != 2 {
		t.Fatalf("databases.json lists %d databases:\n%s", len(file.Databases), text)
	}
	for _, db := range file.Databases {
		for key, value := range db {
			if !fields[key] {
				t.Errorf("database %v: livequeryd defines no field %q", db["id"], key)
			}
			if path, ok := value.(string); ok && strings.HasSuffix(key, "_file") {
				cred, found := strings.CutPrefix(path, "${CREDENTIALS_DIRECTORY}/")
				if !found || !creds[cred] {
					t.Errorf("database %v: %s = %q is not a credential the drop-in loads", db["id"], key, path)
				}
			}
		}
	}
	a, b := file.Databases[0], file.Databases[1]
	if a["id"] != "3f1c" || a["slot"] != nil || a["publication"] != "gregale_live" || a["install"] != true || a["release_after"] != nil {
		t.Errorf("first database = %v", a)
	}
	if b["slot"] != "live_b" || b["release_after"] != "5m" || b["awake"] != "neon" || b["neon_endpoint"] != "ep-1" {
		t.Errorf("second database = %v", b)
	}
}

// adr: 20261016151947 — realtimed on compute nodes reaches the hub; nothing
// else does. The rule exists only when the hub is managed.
func TestLivequerydFirewallStandardJinja(t *testing.T) {
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for firewall rendering")
	}
	template := filepath.Join("..", "..", "deploy", "ansible", "roles", "nftables", "templates", "policy_nftables.conf.j2")
	script := `import pathlib, sys
from jinja2 import Template
print(Template(pathlib.Path(sys.argv[1]).read_text()).render(public_iface="eth0", masquerade_cidr="10.100.0.0/16",
    faas_livequeryd_managed=sys.argv[2], faas_compute_allowed_cidrs=["10.0.1.0/24"], faas_compute_allowed_ports=[9091]), end="")
`
	rule := `ip saddr 10.0.1.0/24 tcp dport 9480 accept comment "live-query hub from realtimed (ADR-20261016151947)"`
	for value, want := range map[string]bool{"true": true, "True": true, "false": false, "False": false} {
		output, err := exec.Command("python3", "-c", script, template, value).CombinedOutput()
		if err != nil {
			t.Fatalf("standard Jinja render failed: %v: %s", err, output)
		}
		if got := strings.Contains(string(output), rule); got != want {
			t.Errorf("faas_livequeryd_managed=%s: hub rule present=%v, want %v", value, got, want)
		}
	}
}

// adr: 20261016151947 — with faas_realtime_live_tls_managed, realtimed's
// drop-in points at the fleet PKI's live client leaf and verifies hubs as
// livequeryd.faas; every variable it sets is one cmd/realtimed reads.
func TestRealtimedLiveTLSDropIn(t *testing.T) {
	tmpl, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "gatewayd_internal_service", "templates", "99-faas-realtimed-role.conf.j2"))
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "cmd", "realtimed", "live.go"))
	if err != nil {
		t.Fatal(err)
	}
	managed := renderPublicEdgeBlock(t, string(tmpl), map[string]any{"faas_box_role": "compute-only", "faas_realtime_live_tls_managed": true})
	for _, want := range []string{
		"Environment=FAAS_REALTIME_LIVE_TLS_CA_FILE=/etc/faas/tls/ca/ca.crt",
		"Environment=FAAS_REALTIME_LIVE_TLS_CERT_FILE=/etc/faas/tls/realtimed/livequeryd-client.crt",
		"Environment=FAAS_REALTIME_LIVE_TLS_KEY_FILE=/etc/faas/tls/realtimed/livequeryd-client.key",
		"Environment=FAAS_REALTIME_LIVE_TLS_SERVER_NAME=livequeryd.faas",
	} {
		if !strings.Contains(managed, want+"\n") {
			t.Errorf("managed drop-in lacks %q:\n%s", want, managed)
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(want, "Environment="), "=")
		if !strings.Contains(string(src), `"`+name+`"`) {
			t.Errorf("realtimed does not read %s", name)
		}
	}
	if off := renderPublicEdgeBlock(t, string(tmpl), map[string]any{"faas_box_role": "compute-only"}); strings.Contains(off, "FAAS_REALTIME_LIVE_TLS") {
		t.Errorf("unmanaged drop-in without paths sets live TLS:\n%s", off)
	}
}
