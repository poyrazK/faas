package api

import (
	"bytes"
	"strings"
	"testing"
)

func TestManifestDefaults(t *testing.T) {
	m := AppManifest{Entrypoint: []string{"/app/server"}}
	if m.EffectivePort() != DefaultAppPort {
		t.Errorf("port default = %d, want %d", m.EffectivePort(), DefaultAppPort)
	}
	if m.EffectiveUser() != DefaultAppUser {
		t.Errorf("user default = %q, want %q", m.EffectiveUser(), DefaultAppUser)
	}
	if m.EffectiveWorkingDir() != "/" {
		t.Errorf("workdir default = %q, want /", m.EffectiveWorkingDir())
	}
}

func TestManifestValidate(t *testing.T) {
	tests := []struct {
		name string
		m    AppManifest
		ok   bool
	}{
		{"valid", AppManifest{Entrypoint: []string{"node", "index.js"}}, true},
		{"empty entrypoint", AppManifest{}, false},
		{"empty argv0", AppManifest{Entrypoint: []string{""}}, false},
		{"bad port", AppManifest{Entrypoint: []string{"x"}, Port: 70000}, false},
		{"neg port", AppManifest{Entrypoint: []string{"x"}, Port: -1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.m.Validate()
			if (err == nil) != tt.ok {
				t.Errorf("Validate() err=%v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestManifestRoundTrip(t *testing.T) {
	in := AppManifest{
		Entrypoint: []string{"node", "server.js"},
		Env:        map[string]string{"NODE_ENV": "production"},
		Port:       3000,
		Healthz:    "/healthz",
	}
	var buf bytes.Buffer
	if err := WriteManifest(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, err := ReadManifest(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if out.Entrypoint[1] != "server.js" || out.Port != 3000 || out.Env["NODE_ENV"] != "production" {
		t.Errorf("round trip mismatch: %+v", out)
	}
}

func TestReadManifestRejectsInvalid(t *testing.T) {
	if _, err := ReadManifest(strings.NewReader(`{"port":3000}`)); err == nil {
		t.Error("manifest with no entrypoint should fail validation on read")
	}
}

func TestErrAppLayerTooLarge(t *testing.T) {
	l := MustLimitsFor(PlanFree) // 256 MB cap
	p := ErrAppLayerTooLarge(l, 300*1024*1024)
	if p.Code != CodeAppLayerTooBig {
		t.Errorf("code = %q", p.Code)
	}
	if p.Limit == nil || *p.Limit != 256*1024*1024 {
		t.Errorf("limit not set to plan cap bytes: %v", p.Limit)
	}
	if !strings.Contains(p.Detail, "256 MB") {
		t.Errorf("detail should name the cap: %q", p.Detail)
	}
}
