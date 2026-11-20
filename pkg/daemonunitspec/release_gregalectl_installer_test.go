// adr: 1344 — a control plane rebuilt from backups installs a verified gregalectl before its first activation (§8).
package daemonunitspec

import (
	"regexp"
	"testing"
)

// deploy/scripts/install-release-gregalectl.sh verifies a release the way
// cd-controlplane does, so its pinned cosign must be the one CD pins and it
// must check the release workflow's identity for the tag.
func TestReleaseGregalectlInstallerVerifiesLikeCD(t *testing.T) {
	script := readRepoFile(t, "deploy/scripts/install-release-gregalectl.sh")
	workflow := readRepoFile(t, ".github/workflows/cd-controlplane.yml")
	for _, pin := range []string{"COSIGN_VERSION", "COSIGN_SHA256"} {
		want := regexp.MustCompile(pin + `: "([^"]+)"`).FindStringSubmatch(workflow)
		got := regexp.MustCompile(pin + `="([^"]+)"`).FindStringSubmatch(script)
		if want == nil || got == nil {
			t.Fatalf("%s: not found in cd-controlplane.yml (%v) or the installer (%v)", pin, want != nil, got != nil)
		}
		if got[1] != want[1] {
			t.Errorf("%s: installer pins %q, cd-controlplane.yml pins %q", pin, got[1], want[1])
		}
	}
	for _, must := range []string{
		`--certificate-identity "https://github.com/${repo}/.github/workflows/release.yml@refs/tags/${tag}"`,
		"--certificate-oidc-issuer https://token.actions.githubusercontent.com",
		"sha256sum -c checked",
		`if [[ -e "$current" || -L "$current" ]]; then`,
	} {
		if !regexp.MustCompile(regexp.QuoteMeta(must)).MatchString(script) {
			t.Errorf("the installer no longer contains %s", must)
		}
	}
}
