// Package acceptance contains the opt-in Gregale CI native gate and its recovery
// journal. It is test tooling; no daemon or production admission path imports it.
package acceptance

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/ci"
)

const OptInEnv = "GREGALE_CI_NATIVE_ACCEPTANCE"
const EvidenceEnv = "GREGALE_CI_ACCEPTANCE_EVIDENCE"

var hexSHA = regexp.MustCompile(`^[0-9a-f]{64}$`)
var sourceSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
var schemaName = regexp.MustCompile(`^ci_acceptance_[0-9a-f]{32}$`)

// Secrets and credential selectors are never part of JSON evidence.
type Config struct {
	DatabaseURL string `json:"-"`
	TLSCert     string `json:"-"`
	TLSKey      string `json:"-"`
	Origin      string
	Listen      string
	Bucket      string
	Image       string
	SourceSHA   string
	RunnerSHA   string
	GuestSHA    string
}

func Load(getenv func(string) string) (Config, error) {
	c := Config{
		DatabaseURL: getenv("GREGALE_CI_ACCEPTANCE_DATABASE_URL"),
		TLSCert:     getenv("GREGALE_CI_ACCEPTANCE_TLS_CERT"), TLSKey: getenv("GREGALE_CI_ACCEPTANCE_TLS_KEY"),
		Origin: getenv("GREGALE_CI_ACCEPTANCE_ORIGIN"), Listen: getenv("GREGALE_CI_ACCEPTANCE_LISTEN"),
		Bucket: getenv("GREGALE_CI_ACCEPTANCE_GCS_BUCKET"), Image: getenv("GREGALE_CI_ACCEPTANCE_IMAGE"),
		SourceSHA: getenv("GREGALE_CI_ACCEPTANCE_SOURCE_SHA"), RunnerSHA: getenv("GREGALE_CI_ACCEPTANCE_RUNNER_SHA256"),
		GuestSHA: getenv("GREGALE_CI_ACCEPTANCE_GUEST_SHA256"),
	}
	if getenv(OptInEnv) != "1" || getenv("STORAGE_EMULATOR_HOST") != "" || c.DatabaseURL == "" || c.TLSCert == "" || c.TLSKey == "" ||
		c.Bucket == "" || strings.TrimSpace(c.Bucket) != c.Bucket || !sourceSHA.MatchString(c.SourceSHA) ||
		!hexSHA.MatchString(c.RunnerSHA) || !hexSHA.MatchString(c.GuestSHA) || !ci.ValidRuntimeOrigin(c.Origin) {
		return Config{}, errors.New("CI native acceptance requires explicit opt-in, dedicated database/bucket, HTTPS credentials and candidate digests")
	}
	u, _ := url.Parse(c.Origin)
	if u.Hostname() == "localhost" || strings.HasSuffix(u.Hostname(), ".localhost") {
		return Config{}, errors.New("CI guest origin must be reachable through normal tenant egress")
	}
	if addr, err := netip.ParseAddr(u.Hostname()); err == nil && (!addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLinkLocalUnicast()) {
		return Config{}, errors.New("CI guest origin cannot use a loopback, private or link-local address")
	}
	_, port, err := net.SplitHostPort(c.Listen)
	expectedPort := u.Port()
	if expectedPort == "" {
		expectedPort = "443"
	}
	if err != nil || port != expectedPort {
		return Config{}, errors.New("CI acceptance listener port must match the HTTPS origin")
	}
	// Use the production definition validator, including immutable OCI refs.
	_, _, err = ci.Compile(ci.Definition{Version: 1, Repository: "gregale/ci-native-fixture", Jobs: map[string]ci.Job{
		"probe": {Image: c.Image, Steps: []ci.Step{{Name: "probe", Run: "exit 0"}}},
	}})
	if err != nil {
		return Config{}, errors.New("CI native acceptance requires a digest-pinned runner image")
	}
	return c, nil
}

func ScenarioNames() []string {
	return []string{"pipeline", "step_failure", "timeout", "cancellation", "restart", "wake_during_ci"}
}
