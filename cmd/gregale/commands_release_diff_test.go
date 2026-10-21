package main_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261021101117 — `gregale deployment diff` prints one fact per line.
func TestReleaseDiffLines(t *testing.T) {
	d := api.ReleaseDiff{App: "shop", Summary: "new code, 1 config key, 1 package, release command changed",
		From: &api.ReleaseDiffSide{DeploymentID: "d41", Revision: 41}, To: api.ReleaseDiffSide{DeploymentID: "d42", Revision: 42},
		Code: api.ReleaseDiffCode{Changed: true, FromCommit: strings.Repeat("a", 40), ToCommit: strings.Repeat("b", 40), CompareURL: "https://github.com/acme/shop/compare/a...b"},
		Config: api.ReleaseDiffConfig{Known: true, Secrets: []api.ReleaseDiffKey{{Key: "TOKEN", Change: api.ReleaseDiffChanged}}},
		Dependencies: api.ReleaseDiffDependencies{Note: "image releases have no build SBOM"},
		Database:     api.ReleaseDiffDatabase{Changed: true, FromReleaseCommand: "node migrate.mjs", ToReleaseCommand: "node migrate.mjs --to 7"},
	}
	out := strings.Join(main.ReleaseDiffLines(d), "\n")
	for _, want := range []string{"shop: v41 (d41) → v42 (d42)", "Summary: new code", "Code: aaaaaaaaaaaa → bbbbbbbbbbbb",
		"https://github.com/acme/shop/compare/a...b", "Secret TOKEN changed", "Packages: unknown (image releases have no build SBOM)",
		"Release command: node migrate.mjs → node migrate.mjs --to 7"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	first := strings.Join(main.ReleaseDiffLines(api.ReleaseDiff{App: "shop", To: api.ReleaseDiffSide{DeploymentID: "d1", Revision: 1}}), "\n")
	if !strings.Contains(first, "first release") {
		t.Fatalf("first release output = %q", first)
	}
}
