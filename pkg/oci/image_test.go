package oci

import (
	"strings"
	"testing"
)

func TestLayersAboveBase(t *testing.T) {
	base := []string{"sha256:aaa", "sha256:bbb"}
	app := []string{"sha256:aaa", "sha256:bbb", "sha256:ccc", "sha256:ddd"}

	above, err := LayersAboveBase(base, app)
	if err != nil {
		t.Fatal(err)
	}
	if len(above) != 2 || above[0] != "sha256:ccc" || above[1] != "sha256:ddd" {
		t.Errorf("above = %v, want [ccc ddd]", above)
	}
}

func TestLayersAboveBaseRejectsMismatch(t *testing.T) {
	// App not built FROM base — must error, never silently proceed.
	base := []string{"sha256:aaa", "sha256:xxx"}
	app := []string{"sha256:aaa", "sha256:bbb", "sha256:ccc"}
	if _, err := LayersAboveBase(base, app); err == nil {
		t.Error("expected error when base is not a prefix of app")
	}
}

func TestLayersAboveBaseRejectsEmptyDiff(t *testing.T) {
	// App identical to base => nothing above => empty app layer is an error.
	base := []string{"sha256:aaa", "sha256:bbb"}
	if _, err := LayersAboveBase(base, base); err == nil {
		t.Error("expected error when app has no layers above base")
	}
}

func TestLayersAboveBaseRejectsShorterApp(t *testing.T) {
	base := []string{"sha256:aaa", "sha256:bbb", "sha256:ccc"}
	app := []string{"sha256:aaa"}
	if _, err := LayersAboveBase(base, app); err == nil {
		t.Error("expected error when app has fewer layers than base")
	}
}

func TestLayersAboveBaseReturnsCopy(t *testing.T) {
	base := []string{"sha256:aaa"}
	app := []string{"sha256:aaa", "sha256:bbb"}
	above, _ := LayersAboveBase(base, app)
	above[0] = "mutated"
	if app[1] == "mutated" {
		t.Error("LayersAboveBase leaked a mutable view into the app slice")
	}
}

func TestParseConfig(t *testing.T) {
	doc := `{
      "config": {
        "Env": ["PATH=/usr/bin", "NODE_ENV=production"],
        "Entrypoint": ["node"],
        "Cmd": ["index.js"],
        "WorkingDir": "/app",
        "User": "1000"
      },
      "rootfs": { "type": "layers", "diff_ids": ["sha256:aaa", "sha256:bbb"] }
    }`
	cfg, err := ParseConfig(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkingDir != "/app" || len(cfg.DiffIDs) != 2 || cfg.Entrypoint[0] != "node" {
		t.Errorf("parsed config unexpected: %+v", cfg)
	}
}

func TestParseConfigRejectsNonLayerRootfs(t *testing.T) {
	if _, err := ParseConfig(strings.NewReader(`{"rootfs":{"type":"foreign"}}`)); err == nil {
		t.Error("expected error on unsupported rootfs type")
	}
}

func TestManifestFromConfig(t *testing.T) {
	cfg := Config{
		Entrypoint: []string{"node"},
		Cmd:        []string{"server.js"},
		Env:        []string{"NODE_ENV=production", "PORT=3000"},
		WorkingDir: "/app",
		User:       "1000",
	}
	m, err := ManifestFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entrypoint) != 2 || m.Entrypoint[1] != "server.js" {
		t.Errorf("entrypoint = %v, want [node server.js]", m.Entrypoint)
	}
	if m.Env["NODE_ENV"] != "production" || m.Env["PORT"] != "3000" {
		t.Errorf("env not flattened: %v", m.Env)
	}
	if m.User != "app" {
		t.Errorf("uid 1000 should normalise to %q, got %q", "app", m.User)
	}
}

func TestManifestFromConfigRejectsNoEntrypoint(t *testing.T) {
	if _, err := ManifestFromConfig(Config{Cmd: nil, Entrypoint: nil}); err == nil {
		t.Error("config with no entrypoint/cmd should fail")
	}
}

func TestNormalizeUser(t *testing.T) {
	tests := map[string]string{
		"":          "",
		"1000":      "app",
		"root":      "root",
		"app:app":   "app",
		"1001":      "1001",
		"node:node": "node",
	}
	for in, want := range tests {
		if got := normalizeUser(in); got != want {
			t.Errorf("normalizeUser(%q) = %q, want %q", in, got, want)
		}
	}
}
