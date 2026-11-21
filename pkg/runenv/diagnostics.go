package runenv

import (
	"github.com/onebox-faas/faas/pkg/api"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var buildStepLine = regexp.MustCompile(`^#[0-9]+\s`)
var diagnosticURL = regexp.MustCompile(`https?://[^\s<>"']+`)
var diagnosticSecret = regexp.MustCompile(`(?i)(authorization|token|password|passwd|secret|api[_-]?key|signature|credential|cookie|set-cookie)\s*["']?\s*[:=]`)
var diagnosticBearer = regexp.MustCompile(`(?i)\b(?:Bearer|Basic)\s+[A-Za-z0-9+/._~=-]+`)
var diagnosticHostPath = regexp.MustCompile(`/(mnt|srv|private|Users|var|home|root)/[^\s"']*`)
var diagnosticANSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

// BuildDiagnosticLine admits package-manager/BuildKit output, excluding host
// boot diagnostics. Auth fields and URL capabilities are never customer logs.
func BuildDiagnosticLine(raw string) string {
	if len(raw) > api.ExecutionEnvironmentDiagnosticMaxLineBytes*4 {
		raw = raw[:api.ExecutionEnvironmentDiagnosticMaxLineBytes*4]
	}
	raw = strings.TrimSpace(diagnosticANSI.ReplaceAllString(raw, ""))
	if !buildStepLine.MatchString(raw) {
		return ""
	}
	raw = diagnosticURL.ReplaceAllStringFunc(raw, func(v string) string {
		u, err := url.Parse(v)
		if err != nil {
			return "[url]"
		}
		u.User = nil
		u.RawQuery = ""
		u.Fragment = ""
		return u.String()
	})
	// Credential values can contain whitespace, quotes and multiple cookies.
	// Discard the remaining line rather than guessing where a secret ends.
	if match := diagnosticSecret.FindStringIndex(raw); match != nil {
		raw = raw[:match[0]] + "[redacted]"
	}
	raw = diagnosticBearer.ReplaceAllString(raw, "[redacted]")
	raw = diagnosticHostPath.ReplaceAllString(raw, "[host-path]")
	raw = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, raw)
	if len(raw) > api.ExecutionEnvironmentDiagnosticMaxLineBytes {
		raw = raw[:api.ExecutionEnvironmentDiagnosticMaxLineBytes]
		for !utf8.ValidString(raw) {
			raw = raw[:len(raw)-1]
		}
	}
	return raw
}
func DiagnosticFailure(raw string) (string, string) {
	s := strings.ToLower(raw)
	switch {
	case strings.Contains(s, "integrity") || strings.Contains(s, "hashes") || strings.Contains(s, "hash mismatch"):
		return "integrity_mismatch", "A dependency did not match its pinned hash. Regenerate the lockfile from a trusted registry."
	case strings.Contains(s, "no matching distribution") || strings.Contains(s, "could not find a version") || strings.Contains(s, "eresolve") || strings.Contains(s, "e404"):
		return "dependency_resolution", "A pinned dependency could not be installed. Check versions, transitive hashes and binary-wheel availability."
	case strings.Contains(s, "deadline") || strings.Contains(s, "timeout"):
		return "build_timeout", "The dependency build reached its time limit. Reduce dependencies or retry when the registry is available."
	case strings.Contains(s, "insufficient host free space") || strings.Contains(s, "dns") || strings.Contains(s, "coldboot") || strings.Contains(s, "spawn"):
		return "builder_unavailable", "The isolated builder could not start or reach the registry. Retry this build or contact support."
	case strings.Contains(s, "space") || strings.Contains(s, "exceed"):
		return "size_limit", "The build exceeded an artifact or workspace limit. Reduce dependencies and inspect your plan limits."
	default:
		return "build_failed", "The isolated dependency build failed. Inspect the package-manager diagnostics and pinned recipe."
	}
}
