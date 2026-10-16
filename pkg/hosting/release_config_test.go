package hosting

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestReleaseConfigSharedContract(t *testing.T) {
	// adr: 202610161909 — release policies are bounded and independently gated.
	data, err := os.ReadFile("../../deploy/cloudflare/hosting-worker/release-config-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name   string
		Config json.RawMessage
		Valid  bool
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	files := []api.HostingFile{{Path: "index.html", ContentType: "text/html"}, {Path: "404.html", ContentType: "text/html; charset=utf-8"}, {Path: "app.js", ContentType: "text/javascript"}}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			cfg, err := ParseReleaseConfig(tc.Config)
			if err == nil {
				err = ValidateReleaseConfig(cfg, files)
			}
			if (err == nil) != tc.Valid {
				t.Fatalf("valid=%v err=%v", tc.Valid, err)
			}
		})
	}
	for _, raw := range []string{"null", "{} {}", "{\"version\":1} trailing", strings.Repeat(" ", api.HostingConfigMaxBytes+1)} {
		if _, err := ParseReleaseConfig([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid JSON %q", raw[:min(len(raw), 30)])
		}
	}
}
