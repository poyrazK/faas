package daemonunitspec

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// envLiteral matches the exact way daemon code spells an env var: a
// double-quoted "FAAS_..." string. A trailing underscore is a prefix the
// code completes at runtime (FAAS_DEPLOY_BASE_REF_<RUNTIME>).
var envLiteral = regexp.MustCompile(`"(FAAS_[A-Z0-9_]+)"`)

// daemonDirs are the cmd/ trees that run on a production host. CLIs
// (gregale, gregalectl, deployctl, ...) are deliberately excluded: they
// run on the operator's machine, and their env is not a deploy concern.
var daemonDirs = []string{
	"cmd/apid", "cmd/schedd", "cmd/vmmd", "cmd/imaged", "cmd/builderd",
	"cmd/meterd", "cmd/githubd", "cmd/gatewayd-internal", "cmd/gatewayd-public",
	"cmd/vmmd-stream-bridge", "cmd/vmmd-raw-bridge",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root not found at %s: %v", root, err)
	}
	return root
}

// scanLiterals returns name -> set of relative files that read it, for
// every non-test .go file under the given dirs.
func scanLiterals(t *testing.T, root string, dirs []string) map[string]map[string]bool {
	t.Helper()
	found := map[string]map[string]bool{}
	for _, d := range dirs {
		err := filepath.WalkDir(filepath.Join(root, d), func(p string, de os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if de.IsDir() {
				// pkg/daemonunitspec is the declaration site (this
				// registry + the unit specs that SET variables); it never
				// reads env, so it must not count as a reader.
				if filepath.Base(p) == "daemonunitspec" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			body, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			for _, m := range envLiteral.FindAllSubmatch(body, -1) {
				name := string(m[1])
				if found[name] == nil {
					found[name] = map[string]bool{}
				}
				found[name][rel] = true
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", d, err)
		}
	}
	return found
}

func ownerOf(rel string) string {
	switch {
	case strings.HasPrefix(rel, "cmd/"):
		return strings.SplitN(strings.TrimPrefix(rel, "cmd/"), "/", 2)[0]
	case strings.HasPrefix(rel, "guest/"):
		return "guest"
	default:
		return "shared"
	}
}

func readTree(t *testing.T, root string, dirs []string, keep func(string) bool) string {
	t.Helper()
	var b strings.Builder
	for _, d := range dirs {
		err := filepath.WalkDir(filepath.Join(root, d), func(p string, de os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if de.IsDir() || strings.Contains(p, "/.generated/") {
				return nil
			}
			if keep != nil && !keep(p) {
				return nil
			}
			body, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			b.Write(body)
			b.WriteByte('\n')
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", d, err)
		}
	}
	return b.String()
}

func renderedUnits() string {
	var b bytes.Buffer
	for _, e := range Registry {
		b.Write(e.Unit().Render())
		b.WriteByte('\n')
	}
	return b.String()
}

func wordRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
}

func TestEnvContract_Sorted(t *testing.T) {
	seen := map[string]bool{}
	for i := 1; i < len(EnvContract); i++ {
		if EnvContract[i-1].Name >= EnvContract[i].Name {
			t.Errorf("EnvContract not sorted at %q (after %q)", EnvContract[i].Name, EnvContract[i-1].Name)
		}
	}
	for _, v := range EnvContract {
		if seen[v.Name] {
			t.Errorf("duplicate entry %q", v.Name)
		}
		seen[v.Name] = true
		if len(v.Owners) == 0 {
			t.Errorf("%s: no owners", v.Name)
		}
		if v.Source == EnvSourceSecretsEnv && !strings.Contains(v.Note, "/etc/faas/") {
			t.Errorf("%s: secrets-env entries must name the delivering file in Note", v.Name)
		}
	}
}

// TestEnvContract_EveryReadIsDeclared is the forward tripwire: a daemon
// cannot start reading a FAAS_* variable without the contract saying who
// sets it.
func TestEnvContract_EveryReadIsDeclared(t *testing.T) {
	root := repoRoot(t)
	reads := scanLiterals(t, root, append(append([]string{}, daemonDirs...), "pkg", "guest"))
	byName := EnvContractByName()
	var missing []string
	for name, files := range reads {
		entry, ok := byName[name]
		if !ok {
			var fs []string
			for f := range files {
				fs = append(fs, f)
			}
			sort.Strings(fs)
			missing = append(missing, fmt.Sprintf("%s (read in %s)", name, strings.Join(fs, ", ")))
			continue
		}
		// Every direct reader must be listed as an owner so the generated
		// doc stays truthful.
		owners := map[string]bool{}
		for _, o := range entry.Owners {
			owners[o] = true
		}
		for f := range files {
			o := ownerOf(f)
			if !owners[o] {
				t.Errorf("%s: read by %s but Owners=%v does not list %q", name, f, entry.Owners, o)
			}
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("undeclared env var: %s\n  → add it to pkg/daemonunitspec/envcontract.go with an honest Source (ADR-143)", m)
	}
}

// TestEnvContract_NoStaleEntries: every declared variable is still read
// by something, so the doc never advertises a knob that does nothing.
func TestEnvContract_NoStaleEntries(t *testing.T) {
	root := repoRoot(t)
	reads := scanLiterals(t, root, append(append([]string{}, daemonDirs...), "pkg", "guest"))
	// Script consumers: shell under deploy/scripts, shell embedded in role
	// tasks (postgres archive_command), and ExecStart lines in units.
	scripts := readTree(t, root, []string{"deploy/scripts", "deploy/ansible/roles", "deploy/systemd"}, func(p string) bool {
		return strings.HasSuffix(p, ".sh") || strings.HasSuffix(p, ".service") ||
			(strings.Contains(p, "/tasks/") && strings.HasSuffix(p, ".yml"))
	})
	for _, v := range EnvContract {
		if v.Source == EnvSourceScript {
			if !wordRe(v.Name).MatchString(scripts) {
				t.Errorf("%s: Source=script but no deploy/scripts/*.sh reads it", v.Name)
			}
			continue
		}
		if _, ok := reads[v.Name]; !ok {
			t.Errorf("%s: declared but no daemon/pkg/guest code reads it — delete the entry", v.Name)
		}
	}
}

// TestEnvContract_DeliveryIsWired is the outage tripwire: an entry that
// promises delivery must actually be delivered by that path.
func TestEnvContract_DeliveryIsWired(t *testing.T) {
	root := repoRoot(t)
	units := renderedUnits()
	ansible := readTree(t, root, []string{"deploy/ansible"}, func(p string) bool {
		return !strings.HasSuffix(p, ".md")
	})
	renderers := readTree(t, root, []string{"cmd/gregalectl", "pkg/manifest", "pkg/renderer"}, func(p string) bool {
		return strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go")
	})
	runtimeCfg, err := os.ReadFile(filepath.Join(root, "cmd/apid/runtime_config.go"))
	if err != nil {
		t.Fatal(err)
	}
	unitEnv := regexp.MustCompile(`(?m)^Environment=(FAAS_[A-Z0-9_]+)=`)
	setInUnits := map[string]bool{}
	for _, m := range unitEnv.FindAllStringSubmatch(units, -1) {
		setInUnits[m[1]] = true
	}
	byDaemon := map[string]string{}
	for _, e := range Registry {
		byDaemon[e.Name] = string(e.Unit().Render())
	}

	for _, v := range EnvContract {
		switch v.Source {
		case EnvSourceUnit:
			if !setInUnits[v.Name] {
				t.Errorf("%s: Source=unit but no pkg/daemonunitspec unit sets Environment=%s=", v.Name, v.Name)
			}
		case EnvSourceDropin:
			if !wordRe(v.Name).MatchString(ansible) {
				t.Errorf("%s: Source=dropin but nothing under deploy/ansible mentions it", v.Name)
			}
		case EnvSourceEnvFile:
			if !wordRe(v.Name).MatchString(ansible) && !wordRe(v.Name).MatchString(renderers) {
				t.Errorf("%s: Source=envfile but neither deploy/ansible nor the manifest renderer stages it", v.Name)
			}
		case EnvSourceSecretsEnv:
			// The delivering EnvironmentFile must exist on each owner's unit.
			for _, o := range v.Owners {
				u, ok := byDaemon[o]
				if !ok {
					continue // "shared" / "guest"
				}
				if !strings.Contains(u, "/etc/faas/secrets/") && !strings.Contains(u, "/etc/faas/sealed.env") {
					t.Errorf("%s: Source=secrets-env but faas-%s.service loads no secrets EnvironmentFile", v.Name, o)
				}
			}
		case EnvSourceRuntimeConfig:
			if !bytes.Contains(runtimeCfg, []byte(`"`+v.Name+`"`)) {
				t.Errorf("%s: Source=runtime-config but cmd/apid/runtime_config.go does not map it", v.Name)
			}
		}
	}
}

// TestEnvContract_NoDeadDeployConfig is the reverse tripwire: everything
// the deploy tree sets must be read by something.
func TestEnvContract_NoDeadDeployConfig(t *testing.T) {
	root := repoRoot(t)
	byName := EnvContractByName()
	declared := func(name string) bool {
		if _, ok := byName[name]; ok {
			return true
		}
		for _, v := range EnvContract {
			if strings.HasSuffix(v.Name, "_") && strings.HasPrefix(name, v.Name) {
				return true
			}
		}
		return false
	}
	setRe := regexp.MustCompile(`(?m)^\s*(?:Environment=)?(FAAS_[A-Z0-9_]+)=`)
	sources := map[string]string{
		"rendered units": renderedUnits(),
		"ansible drop-ins": readTree(t, root, []string{"deploy/ansible/roles"}, func(p string) bool {
			return strings.HasSuffix(p, ".conf.j2") || strings.HasSuffix(p, ".conf") || strings.HasSuffix(p, ".env.example")
		}),
	}
	for label, body := range sources {
		seen := map[string]bool{}
		for _, m := range setRe.FindAllStringSubmatch(body, -1) {
			name := m[1]
			if seen[name] {
				continue
			}
			seen[name] = true
			if !declared(name) {
				t.Errorf("%s set %s but no daemon, script or guest reads it (dead config) — delete it or declare it in envcontract.go", label, name)
			}
		}
	}
}

// TestEnvContract_DocInSync keeps docs/ops/env-contract.md generated from
// the registry. Run with UPDATE_ENV_CONTRACT=1 to rewrite it.
func TestEnvContract_DocInSync(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "docs", "ops", "env-contract.md")
	want := renderEnvContractDoc()
	if os.Getenv("UPDATE_ENV_CONTRACT") == "1" {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run: UPDATE_ENV_CONTRACT=1 go test ./pkg/daemonunitspec -run DocInSync)", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s is out of date — run: UPDATE_ENV_CONTRACT=1 go test ./pkg/daemonunitspec -run DocInSync", path)
	}
}

func renderEnvContractDoc() string {
	var b strings.Builder
	b.WriteString("# Daemon environment contract\n\n")
	b.WriteString("<!-- GENERATED from pkg/daemonunitspec/envcontract.go — do not edit. -->\n")
	b.WriteString("<!-- Regenerate: UPDATE_ENV_CONTRACT=1 go test ./pkg/daemonunitspec -run DocInSync -->\n\n")
	b.WriteString("Every `FAAS_*` variable a production daemon reads, and which deploy path\n")
	b.WriteString("delivers it. Enforced by `pkg/daemonunitspec/envcontract_test.go` (ADR-143).\n\n")
	b.WriteString("| Source | Meaning |\n|---|---|\n")
	for _, s := range []struct {
		src  EnvSource
		desc string
	}{
		{EnvSourceUnit, "`Environment=` in the daemon's generated systemd unit"},
		{EnvSourceDropin, "systemd drop-in rendered by an ansible role"},
		{EnvSourceEnvFile, "`EnvironmentFile` staged by ansible, node_join or the manifest renderer"},
		{EnvSourceSecretsEnv, "operator-provisioned secret file under `/etc/faas/secrets/` or `/etc/faas/sealed.env`"},
		{EnvSourceRuntimeConfig, "DB-backed runtime config; env is only the boot default"},
		{EnvSourceDefault, "optional; the code default is production-correct"},
		{EnvSourceScript, "consumed by a `deploy/scripts/` script"},
		{EnvSourceInternal, "set by a daemon for its own subprocess"},
		{EnvSourceClient, "read by the CLI/SDK on the operator's machine"},
		{EnvSourceDevOnly, "tests / e2e / Lima only — never set in production"},
		{EnvSourceGuest, "read by guest-init inside the microVM"},
	} {
		fmt.Fprintf(&b, "| `%s` | %s |\n", s.src, s.desc)
	}
	b.WriteString("\n| Variable | Owners | Source | Note |\n|---|---|---|---|\n")
	for _, v := range EnvContract {
		note := strings.ReplaceAll(v.Note, "|", "\\|")
		fmt.Fprintf(&b, "| `%s` | %s | `%s` | %s |\n", v.Name, strings.Join(v.Owners, ", "), v.Source, note)
	}
	return b.String()
}
