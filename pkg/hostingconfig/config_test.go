package hostingconfig

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadHostingOverrides(t *testing.T) {
	cfg, ok, err := Load(fstest.MapFS{
		"gregale.yaml": &fstest.MapFile{Data: []byte(`hosting:
  start: "npm run serve"
  port: 8787
  health: /ready
triggers: []
`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok || cfg.Start != "npm run serve" || cfg.Port != 8787 || cfg.Health != "/ready" {
		t.Fatalf("config = %+v, present=%v", cfg, ok)
	}
}

func TestParseRejectsUnknownHostingField(t *testing.T) {
	_, _, err := Parse([]byte("hosting:\n  command: node server.js\n"))
	if err == nil || !strings.Contains(err.Error(), "field command not found") {
		t.Fatalf("error = %v, want unknown-field error", err)
	}
}

func TestParseRejectsInvalidValues(t *testing.T) {
	cases := []string{
		"hosting:\n  port: 70000\n",
		"hosting:\n  health: ready\n",
		"hosting:\n  start: \"node\\nserver.js\"\n",
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			_, _, err := Parse([]byte(body))
			if err == nil {
				t.Fatal("Parse returned nil error")
			}
		})
	}
}

func TestLoadWithoutHostingIsNoop(t *testing.T) {
	cfg, ok, err := Load(fstest.MapFS{
		"gregale.yaml": &fstest.MapFile{Data: []byte("triggers: []\n")},
	})
	if err != nil || ok || cfg != (Config{}) {
		t.Fatalf("config=%+v present=%v err=%v, want empty no-op", cfg, ok, err)
	}
}
