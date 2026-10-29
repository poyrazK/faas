// adr: 122, 531
package hostidentity

import (
	"strconv"
	"strings"
)

// BuildDeploymentHost is the shared writer counterpart to the runtime parser.
// Preserve legacy parser grammar rather than adding allocation-only DNS checks.
func BuildDeploymentHost(deploySuffix string, revision int, slug string) string {
	if deploySuffix == "" || !strings.HasPrefix(deploySuffix, ".") || revision <= 0 || slug == "" {
		return ""
	}
	host := "deploy-" + strconv.Itoa(revision) + "-" + slug + deploySuffix
	parsed, parsedSlug, ok := DeploymentScopeFromHost(deploySuffix, host)
	if !ok || parsed != revision || parsedSlug != slug {
		return ""
	}
	return host
}

// DeploymentScopeFromHost peels the canonical deploy-{ordinal}-{slug} URL.
func DeploymentScopeFromHost(deploySuffix, host string) (ordinal int, slug string, ok bool) {
	if deploySuffix == "" {
		return 0, "", false
	}
	label, ok := strings.CutSuffix(host, deploySuffix)
	if !ok || label == "" {
		return 0, "", false
	}
	if !strings.HasPrefix(label, "deploy-") {
		return 0, "", false
	}
	tail := label[7:]
	// No inner dots: the slug must not contain a separator (the
	// platform slug charset already excludes dots; this guard
	// rejects pathological scans like `deploy-42-foo.bar.gregale.dev`
	// whose label is "deploy-42-foo.bar" and would otherwise split as
	// slug="42.foo").
	if strings.Contains(tail, ".") {
		return 0, "", false
	}
	// Deployment previews use the same one-label grammar as PR previews:
	// deploy-{N}-{slug}. Cut on the FIRST '-' after the digits so a
	// slug containing '-' is honored verbatim. Keeping the whole preview
	// name in one label is required for the platform *.gregale.dev cert.
	dash := strings.IndexByte(tail, '-')
	if dash <= 0 || dash == len(tail)-1 {
		return 0, "", false
	}
	digits := tail[:dash]
	if digits[0] == '0' && len(digits) > 1 {
		return 0, "", false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, "", false
		}
	}
	rest := tail[dash+1:]
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if c < 'a' || c > 'z' {
			if c < '0' || c > '9' {
				if c != '-' {
					return 0, "", false
				}
			}
		}
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n <= 0 {
		return 0, "", false
	}
	return n, rest, true
}
