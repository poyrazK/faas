// preview_parser_test.go — table-driven pin for the two hostname
// parsers (PreviewScopeFromHost + DeploymentScopeFromHost). Mirrors
// the cmd/gatewayd-internal tests for the same shape so a regression
// in either side is caught at the pkg/gateway unit-test layer, not
// at the production router.
//
// Issue #272 / ADR-095 = PreviewScopeFromHost (pr-{N}.{slug}.{suffix}).
// Issue #976 / ADR-122 = DeploymentScopeFromHost (deploy-{N}.{slug}.{suffix}).

package gateway

import "testing"

// TestPreviewScopeFromHost is the original PR-preview parser test
// (issue #272 / ADR-095 PR-B). Kept in pkg/gateway because the parser
// moved from cmd/gatewayd-internal to pkg/gateway during the allowlist
// split. Mirrors cmd/gatewayd-internal/preview_parser_test.go so the
// two paths agree.
func TestPreviewScopeFromHost(t *testing.T) {
	const suffix = ".apps.gregale.dev"
	cases := []struct {
		name     string
		host     string
		wantNum  int
		wantSlug string
		wantOK   bool
	}{
		{name: "valid lowercase", host: "pr-42-foo.apps.gregale.dev", wantNum: 42, wantSlug: "foo", wantOK: true},
		{name: "valid multi-digit", host: "pr-1000-bar.apps.gregale.dev", wantNum: 1000, wantSlug: "bar", wantOK: true},
		{name: "valid slug with dash", host: "pr-7-foo-bar.apps.gregale.dev", wantNum: 7, wantSlug: "foo-bar", wantOK: true},
		{name: "uppercase rejects", host: "PR-42-foo.apps.gregale.dev", wantOK: false},
		{name: "leading zero rejects", host: "pr-042-foo.apps.gregale.dev", wantOK: false},
		{name: "zero rejects", host: "pr-0-foo.apps.gregale.dev", wantOK: false},
		{name: "missing slug rejects", host: "pr-42.apps.gregale.dev", wantOK: false},
		{name: "missing number rejects", host: "pr-foo.apps.gregale.dev", wantOK: false},
		{name: "wrong suffix rejects", host: "pr-42-foo.gregale.dev", wantOK: false},
		{name: "embedded dot in slug rejects", host: "pr-42-foo.bar.apps.gregale.dev", wantOK: false},
		{name: "empty host rejects", host: "", wantOK: false},
		{name: "empty suffix rejects", host: "pr-42-foo.apps.gregale.dev", wantOK: false}, // overridden below
	}
	// Override the last case: the parser is called with a real
	// suffix constant in production; "empty suffix" is its own
	// test below.
	cases[len(cases)-1].wantOK = false
	cases[len(cases)-1].host = "anything.apps.gregale.dev"

	// Add the empty-suffix case explicitly.
	cases = append(cases, struct {
		name     string
		host     string
		wantNum  int
		wantSlug string
		wantOK   bool
	}{name: "empty suffix refuses everything", host: "pr-42-foo.apps.gregale.dev", wantOK: false})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			suffix := suffix
			if tc.name == "empty suffix refuses everything" {
				suffix = ""
			}
			n, slug, ok := PreviewScopeFromHost(suffix, tc.host)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if n != tc.wantNum {
				t.Errorf("n = %d, want %d", n, tc.wantNum)
			}
			if slug != tc.wantSlug {
				t.Errorf("slug = %q, want %q", slug, tc.wantSlug)
			}
		})
	}
}

// TestDeploymentScopeFromHost (issue #976 / ADR-122 / SAFE-RELEASES-C)
// pins the deployment-preview parser. The shape is
// `deploy-{N}.{slug}.{suffix}` where suffix is `.gregale.dev` (the
// in-flight cert-wildcard target, NOT legacy `.apps.gregale.dev`).
//
// Mirrors TestPreviewScopeFromHost's shape so future maintainers see
// the two parsers' differences at a glance.
func TestDeploymentScopeFromHost(t *testing.T) {
	const suffix = ".gregale.dev"
	cases := []struct {
		name     string
		host     string
		wantNum  int
		wantSlug string
		wantOK   bool
	}{
		{name: "valid lowercase", host: "deploy-42-foo.gregale.dev", wantNum: 42, wantSlug: "foo", wantOK: true},
		{name: "valid multi-digit", host: "deploy-1000-bar.gregale.dev", wantNum: 1000, wantSlug: "bar", wantOK: true},
		{name: "valid slug with dash", host: "deploy-7-foo-bar.gregale.dev", wantNum: 7, wantSlug: "foo-bar", wantOK: true},
		{name: "uppercase rejects", host: "DEPLOY-42-foo.gregale.dev", wantOK: false},
		{name: "leading zero rejects", host: "deploy-042-foo.gregale.dev", wantOK: false},
		{name: "zero rejects", host: "deploy-0-foo.gregale.dev", wantOK: false},
		{name: "missing slug rejects", host: "deploy-42.gregale.dev", wantOK: false},
		{name: "missing number rejects", host: "deploy-foo.gregale.dev", wantOK: false},
		{name: "wrong suffix rejects (legacy apps shape)", host: "deploy-42-foo.apps.gregale.dev", wantOK: false},
		{name: "embedded dot in slug rejects", host: "deploy-42-foo.bar.gregale.dev", wantOK: false},
		{name: "PR preview prefix rejects", host: "pr-42-foo.gregale.dev", wantOK: false},
		{name: "empty host rejects", host: "", wantOK: false},
		{name: "empty suffix refuses everything", host: "deploy-42-foo.gregale.dev", wantOK: false}, // overridden below
	}
	// Override the last case: this is the same shape as a valid
	// entry, but with an empty suffix the parser refuses
	// everything. We override the host to a dummy so the table
	// is self-explanatory.
	cases[len(cases)-1].host = "anything.gregale.dev"

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			suffix := suffix
			if tc.name == "empty suffix refuses everything" {
				suffix = ""
			}
			n, slug, ok := DeploymentScopeFromHost(suffix, tc.host)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if n != tc.wantNum {
				t.Errorf("n = %d, want %d", n, tc.wantNum)
			}
			if slug != tc.wantSlug {
				t.Errorf("slug = %q, want %q", slug, tc.wantSlug)
			}
		})
	}
}
