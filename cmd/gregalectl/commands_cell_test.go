package main

import (
	"bytes"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/manifest"
	"gopkg.in/yaml.v3"
)

func TestControlPlaneCellAnsibleInventory(t *testing.T) {
	cell := db.CellIdentity{ID: uuid.NewString(), Region: "eu-fsn"}
	file := cellCLIManifest(t, cell)
	m, err := manifest.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	m.Fleet.Hosts = append(m.Fleet.Hosts, manifest.Host{Name: "fsn-worker", Role: "compute-only", Address: "10.42.0.2:50051", Region: cell.Region})
	for _, bound := range []bool{true, false} {
		if !bound {
			m.ControlPlaneCell = nil
		}
		files, err := renderManifestAnsibleFiles(m, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, file := range files {
			if filepath.Ext(file.Path) != ".yml" {
				continue
			}
			var vars struct {
				ID     string   `yaml:"faas_control_plane_cell_id"`
				Region string   `yaml:"faas_control_plane_cell_region"`
				Units  []string `yaml:"faas_control_plane_cell_units"`
			}
			if err := yaml.Unmarshal(file.Body, &vars); err != nil {
				t.Fatal(err)
			}
			if bound && (vars.ID != cell.ID || vars.Region != cell.Region) || !bound && (vars.ID != "" || vars.Region != "") || len(vars.Units) == 0 {
				t.Fatalf("bound=%t host vars=%+v", bound, vars)
			}
			if strings.HasSuffix(file.Path, "fsn-1.yml") && (!strings.Contains(string(file.Body), "faas-bridged.service") || !strings.Contains(string(file.Body), "faas-s3-gatewayd.service")) {
				t.Fatal("optional control-plane services lost database identity")
			}
			count++
		}
		if count != 2 {
			t.Fatalf("host inventories=%d", count)
		}
	}
}

func cellCLIManifest(t *testing.T, cell db.CellIdentity) string {
	t.Helper()
	body := strings.Replace(validManifestYAML, "fleet:\n", "control_plane_cell:\n  id: "+cell.ID+"\n  region: "+cell.Region+"\nfleet:\n", 1)
	body = strings.Replace(body, "      role: control-plane\n", "      role: control-plane\n      region: "+cell.Region+"\n      address: 10.42.0.1:7100\n", 1)
	return writeSplitboxManifest(t, body)
}

func TestControlPlaneCellCLIInitAndStatus(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	cfg := pool.Config().ConnConfig
	u, err := url.Parse(cfg.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + cfg.Database
	q := u.Query()
	if searchPath := cfg.RuntimeParams["search_path"]; searchPath != "" {
		q.Set("search_path", searchPath)
	}
	u.RawQuery = q.Encode()
	t.Setenv("DATABASE_URL", u.String())
	cell := db.CellIdentity{ID: uuid.NewString(), Region: "eu-fsn"}
	manifestFile := cellCLIManifest(t, cell)
	var out, stderr bytes.Buffer
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	osStdout, osStderr, jsonOutput = &out, &stderr, true
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
	for _, command := range []string{"init", "init", "status"} {
		out.Reset()
		if code := cmdCellDispatch([]string{command, "--manifest-file", manifestFile}); code != 0 {
			t.Fatalf("%s exit=%d stderr=%s", command, code, stderr.String())
		}
		var actual db.CellIdentity
		if err := json.Unmarshal(out.Bytes(), &actual); err != nil || actual != cell {
			t.Fatalf("%s output=%s err=%v", command, out.String(), err)
		}
	}
	foreign := cellCLIManifest(t, db.CellIdentity{ID: uuid.NewString(), Region: "eu-hel"})
	for _, command := range []string{"init", "status"} {
		if code := cmdCellDispatch([]string{command, "--manifest-file", foreign}); code != 3 {
			t.Fatalf("foreign %s exit=%d", command, code)
		}
	}
}

func TestControlPlaneCellCLIRequiresExplicitDestination(t *testing.T) {
	var out bytes.Buffer
	oldErr := osStderr
	osStderr = &out
	t.Cleanup(func() { osStderr = oldErr })
	file := cellCLIManifest(t, db.CellIdentity{ID: uuid.NewString(), Region: "eu-fsn"})
	for _, args := range [][]string{nil, {"unknown"}, {"init"}, {"status", "--manifest-file", file, "extra"}} {
		if code := cmdCellDispatch(args); code != 1 {
			t.Fatalf("args=%v exit=%d", args, code)
		}
	}
	t.Setenv("DATABASE_URL", "")
	if code := cmdCellDispatch([]string{"init", "--manifest-file", file}); code != 3 || !strings.Contains(out.String(), "explicit DATABASE_URL") {
		t.Fatalf("default database accepted: exit=%d stderr=%s", code, out.String())
	}
	out.Reset()
	t.Setenv("DATABASE_URL", "postgres://user:credential-must-not-leak@[broken")
	if code := cmdCellDispatch([]string{"init", "--manifest-file", file}); code != 3 || strings.Contains(out.String(), "credential-must-not-leak") {
		t.Fatalf("invalid database URL diagnostic exit=%d stderr=%s", code, out.String())
	}
	if code := cmdCellDispatch([]string{"--help"}); code != 0 {
		t.Fatalf("help exit=%d", code)
	}
}
