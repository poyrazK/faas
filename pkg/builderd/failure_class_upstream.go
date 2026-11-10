package builderd

import "strings"

// upstreamFetchHosts are the download hosts a source build reaches because
// Gregale's build tooling chose them, not because the customer's build named
// them: Railpack's builder images and its mise bootstrap, the language
// toolchains mise installs, and each ecosystem's default public package
// registry. A transport failure or 5xx from one of them is an upstream outage
// the customer cannot fix by editing source (#6666).
var upstreamFetchHosts = []string{
	// Railpack and its mise bootstrap.
	"ghcr.io/railwayapp", "railwayapp%2f", "github.com/jdx/mise", "mise.jdx.dev", "mise-versions.jdx.dev",
	// Toolchains mise installs.
	"nodejs.org/dist", "go.dev/dl", "dl.google.com/go", "github.com/astral-sh/python-build-standalone",
	"static.rust-lang.org",
	// Default public package registries.
	"proxy.golang.org", "sum.golang.org", "registry.npmjs.org", "registry.yarnpkg.com",
	"pypi.org", "files.pythonhosted.org", "static.crates.io", "index.crates.io",
}

// upstreamFetchMarkers are transport failures and server errors. A 4xx is the
// upstream's answer about what the build asked for (an unknown package or
// version), so it stays the customer's failure.
var upstreamFetchMarkers = append([]string{
	"context deadline exceeded", "temporary failure fetching", "gateway time-out", "network is unreachable",
	"connection timed out", "eai_again", "etimedout", "econnreset", "econnrefused",
}, platformBaseNetworkMarkers...)

// upstreamFetchTailLines bounds how far from the end of the build output the
// failing fetch may be. A fetch that failed the build is the last thing the
// failing step printed; BuildKit's own summary follows it.
const upstreamFetchTailLines = 60

// upstreamFetchFailed reports the host whose download failed a build that
// Gregale's tooling drove, or "" when the output shows no such failure. Only
// the customer's [build] section is read, and retry warnings are skipped: a
// fetch that was retried and then succeeded did not fail the build.
// Dockerfile builds are out of scope: there the customer chose every fetch.
func upstreamFetchFailed(logTail string, dockerfileBuild bool) string {
	if dockerfileBuild {
		return ""
	}
	build, _, _ := strings.Cut(logTail, "\n[buildkitd]\n")
	lines := strings.Split(strings.ToLower(build), "\n")
	kept := 0
	for i := len(lines) - 1; i >= 0 && kept < upstreamFetchTailLines; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		kept++
		if strings.Contains(line, "warn") || strings.Contains(line, "retrying") {
			continue
		}
		host := upstreamFetchHost(line)
		if host == "" {
			continue
		}
		for _, marker := range upstreamFetchMarkers {
			if strings.Contains(line, marker) {
				return host
			}
		}
	}
	return ""
}

func upstreamFetchHost(line string) string {
	for _, host := range upstreamFetchHosts {
		if strings.Contains(line, host) {
			if host == "railwayapp%2f" {
				return "ghcr.io/railwayapp"
			}
			return host
		}
	}
	return ""
}
