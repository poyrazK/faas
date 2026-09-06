package oci

import (
	"errors"
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
	_, err := LayersAboveBase(base, app)
	if err == nil {
		t.Fatal("expected error when base is not a prefix of app")
	}
	// ADR-141 §Decision 3: the prefix-check failure lifts to the
	// typed ErrLayersNotAboveBase sentinel so the imaged
	// dispatch in buildImageLayer can branch via errors.Is.
	if !errors.Is(err, ErrLayersNotAboveBase) {
		t.Errorf("err = %v, want errors.Is(_, ErrLayersNotAboveBase) true", err)
	}
}

func TestLayersAboveBaseRejectsEmptyDiff(t *testing.T) {
	// App identical to base => nothing above => empty app layer is an error.
	base := []string{"sha256:aaa", "sha256:bbb"}
	_, err := LayersAboveBase(base, base)
	if err == nil {
		t.Fatal("expected error when app has no layers above base")
	}
	// ADR-141 §Decision 3: empty-above is the same dispatch class
	// as not-a-prefix — both lift to ErrLayersNotAboveBase.
	if !errors.Is(err, ErrLayersNotAboveBase) {
		t.Errorf("err = %v, want errors.Is(_, ErrLayersNotAboveBase) true", err)
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

func TestSingleTCPExposedPort(t *testing.T) {
	tests := []struct {
		name    string
		exposed map[string]struct{}
		want    int
		ok      bool
	}{
		{name: "single tcp", exposed: map[string]struct{}{"3000/tcp": {}}, want: 3000, ok: true},
		{name: "case insensitive protocol", exposed: map[string]struct{}{"3000/TCP": {}}, want: 3000, ok: true},
		{name: "udp ignored", exposed: map[string]struct{}{"53/udp": {}}, ok: false},
		{name: "multiple tcp ports are ambiguous", exposed: map[string]struct{}{"3000/tcp": {}, "8080/tcp": {}}, ok: false},
		{name: "duplicate port spelling", exposed: map[string]struct{}{"3000/tcp": {}, "3000/TCP": {}}, want: 3000, ok: true},
		{name: "malformed entries ignored", exposed: map[string]struct{}{"bad/tcp": {}, "70000/tcp": {}}, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := SingleTCPExposedPort(tt.exposed)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("SingleTCPExposedPort(%v) = (%d, %v), want (%d, %v)", tt.exposed, got, ok, tt.want, tt.ok)
			}
		})
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
		Env:        map[string]string{"NODE_ENV": "production", "PORT": "3000"},
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

func TestManifestFromConfig_ExposedPortSeedsServingPort(t *testing.T) {
	m, err := ManifestFromConfig(Config{
		Cmd:          []string{"/app/server"},
		ExposedPorts: map[string]struct{}{"3000/tcp": {}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Port != 3000 {
		t.Fatalf("Port = %d, want 3000", m.Port)
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
