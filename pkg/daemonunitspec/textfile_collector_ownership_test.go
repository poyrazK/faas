package daemonunitspec

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const textfileCollectorDir = "/var/lib/node_exporter/textfile_collector"

// textfileDirDeclaration is one ansible file task that manages the shared
// node_exporter textfile directory.
type textfileDirDeclaration struct {
	where string
	args  map[string]any
}

// collectTextfileDirDeclarations walks a parsed YAML tree and returns every
// ansible.builtin.file (or file) task whose path is the textfile directory.
func collectTextfileDirDeclarations(where string, node any, out *[]textfileDirDeclaration) {
	switch v := node.(type) {
	case map[string]any:
		for _, key := range []string{"ansible.builtin.file", "file"} {
			if args, ok := v[key].(map[string]any); ok && args["path"] == textfileCollectorDir {
				name, _ := v["name"].(string)
				*out = append(*out, textfileDirDeclaration{where: fmt.Sprintf("%s (%s)", where, name), args: args})
			}
		}
		for _, child := range v {
			collectTextfileDirDeclarations(where, child, out)
		}
	case []any:
		for _, child := range v {
			collectTextfileDirDeclarations(where, child, out)
		}
	}
}

// TestTextfileCollectorDirectoryHasOneOwnerAndMode reproduces production-us
// on 2026-10-05. The postgres role chowned the shared textfile directory to
// postgres and the node_exporter role chowned it back. A control-plane
// convergence that stopped between the two left faas-postgres-connection-
// metrics unable to write its .prom file ("Permission denied"). Every task
// that manages the directory must declare the same owner and mode, so the
// result no longer depends on which role ran last.
func TestTextfileCollectorDirectoryHasOneOwnerAndMode(t *testing.T) {
	root := repoRoot(t)
	ansibleDir := filepath.Join(root, "deploy", "ansible")
	files, err := filepath.Glob(filepath.Join(ansibleDir, "roles", "*", "tasks", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	playbooks, err := filepath.Glob(filepath.Join(ansibleDir, "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, playbooks...)

	var decls []textfileDirDeclaration
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(body, []byte(textfileCollectorDir)) {
			continue
		}
		var tree any
		if err := yaml.Unmarshal(body, &tree); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		rel, _ := filepath.Rel(root, f)
		collectTextfileDirDeclarations(rel, tree, &decls)
	}

	// node_exporter, postgres, canary_artifact_retention and node_join.yml
	// all manage the directory today.
	if len(decls) < 4 {
		t.Fatalf("found %d tasks managing %s, want at least 4; did the walker stop matching?", len(decls), textfileCollectorDir)
	}
	for _, d := range decls {
		if mode, _ := d.args["mode"].(string); mode != "0775" {
			t.Errorf("%s: mode = %q, want \"0775\"", d.where, mode)
		}
		for _, key := range []string{"owner", "group"} {
			if v, ok := d.args[key]; ok && v != "node_exporter" {
				t.Errorf("%s: %s = %v, want node_exporter (or unset)", d.where, key, v)
			}
		}
	}
}

// TestTextfileCollectorWritersCanWriteTheDirectory guards the other half of
// the fix. With the directory node_exporter:node_exporter 0775, a unit that
// writes into it must run as root, as node_exporter, or with node_exporter
// as a supplementary group.
func TestTextfileCollectorWritersCanWriteTheDirectory(t *testing.T) {
	root := repoRoot(t)
	var units []string
	for _, pattern := range []string{"files/*.service", "templates/*.service.j2"} {
		matches, err := filepath.Glob(filepath.Join(root, "deploy", "ansible", "roles", "*", pattern))
		if err != nil {
			t.Fatal(err)
		}
		units = append(units, matches...)
	}

	writers := 0
	for _, unit := range units {
		body, err := os.ReadFile(unit)
		if err != nil {
			t.Fatal(err)
		}
		user, groups, writes := "root", "", false
		scanner := bufio.NewScanner(bytes.NewReader(body))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			switch {
			case strings.HasPrefix(line, "User="):
				user = strings.TrimPrefix(line, "User=")
			case strings.HasPrefix(line, "SupplementaryGroups="):
				groups += " " + strings.TrimPrefix(line, "SupplementaryGroups=")
			case strings.HasPrefix(line, "ReadWritePaths=") && strings.Contains(line, textfileCollectorDir):
				writes = true
			}
		}
		if !writes {
			continue
		}
		writers++
		rel, _ := filepath.Rel(root, unit)
		if user == "root" || user == "node_exporter" {
			continue
		}
		if !strings.Contains(" "+groups+" ", " node_exporter ") {
			t.Errorf("%s runs as %s and writes %s without SupplementaryGroups=node_exporter", rel, user, textfileCollectorDir)
		}
	}
	if writers == 0 {
		t.Fatalf("no unit lists %s in ReadWritePaths; did the scan stop matching?", textfileCollectorDir)
	}
}
