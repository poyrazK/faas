package manifest

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ServingProbes turns on the compute nodes' serving probe and the control
// plane's matching expected-probe inventory (docs/ops/serving-probe.md,
// ADR-20261023124955 §5, ADR-20261112210000). The same policy applies to
// every static compute host; the post-deployment monitoring gate needs it to
// qualify a release. Absent leaves the fleet unconfigured. Each compute node
// also needs the operator-provisioned webhook credential the serving_probe
// role checks.
type ServingProbes struct {
	// URLs are platform-owned probe apps reached through the public edge.
	URLs []string `yaml:"urls"`
	// ExpectedBody is the exact response body every URL must return.
	ExpectedBody string `yaml:"expected_body"`
	// Interval is the systemd timer cadence (5min when empty).
	Interval string `yaml:"interval,omitempty"`
	// TimeoutSeconds bounds one attempt (20 when zero).
	TimeoutSeconds float64 `yaml:"timeout_seconds,omitempty"`
	// MaxFirstByteSeconds is the latency budget (the timeout when zero).
	MaxFirstByteSeconds float64 `yaml:"max_first_byte_seconds,omitempty"`
	// MaxAgeSeconds is the oldest result the coverage rules accept (900 when
	// zero); at least three intervals.
	MaxAgeSeconds float64 `yaml:"max_age_seconds,omitempty"`
}

// The Ansible fleet-policy assertions (roles/_shared/tasks/
// serving_probe_fleet_policy.yml) accept exactly these shapes.
var (
	servingProbeURLPattern      = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[A-Za-z0-9._~!&()*+,;=:@%/?-]*)?$`)
	servingProbeIntervalPattern = regexp.MustCompile(`^([1-9][0-9]*)(s|min|h)$`)
)

const servingProbeExpectedBodyMaxBytes = 4096

func (p *ServingProbes) validate() Errors {
	if p == nil {
		return nil
	}
	var errs Errors
	if len(p.URLs) == 0 {
		errs = append(errs, Error{"serving_probes.urls", "needs at least one probe URL"})
	}
	seen := map[string]bool{}
	for i, u := range p.URLs {
		field := fmt.Sprintf("serving_probes.urls[%d]", i)
		if !servingProbeURLPattern.MatchString(u) {
			errs = append(errs, Error{field, fmt.Sprintf("%q must be an https URL without spaces or a fragment", u)})
		}
		if seen[u] {
			errs = append(errs, Error{field, fmt.Sprintf("%q is listed twice", u)})
		}
		seen[u] = true
	}
	switch {
	case p.ExpectedBody == "":
		errs = append(errs, Error{"serving_probes.expected_body", "is required: the probe needs an exact response body"})
	case len(p.ExpectedBody) > servingProbeExpectedBodyMaxBytes || !utf8.ValidString(p.ExpectedBody):
		errs = append(errs, Error{"serving_probes.expected_body", fmt.Sprintf("must be valid UTF-8 of at most %d bytes", servingProbeExpectedBodyMaxBytes)})
	case strings.IndexFunc(p.ExpectedBody, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0:
		errs = append(errs, Error{"serving_probes.expected_body", "must not contain control characters"})
	}
	interval := 300.0
	if p.Interval != "" {
		m := servingProbeIntervalPattern.FindStringSubmatch(p.Interval)
		if m == nil {
			errs = append(errs, Error{"serving_probes.interval", fmt.Sprintf("%q must look like 30s, 5min or 1h", p.Interval)})
		} else {
			n, _ := strconv.ParseFloat(m[1], 64)
			interval = n * map[string]float64{"s": 1, "min": 60, "h": 3600}[m[2]]
		}
	}
	timeout := servingProbeOr(p.TimeoutSeconds, 20)
	if !servingProbeFinite(timeout) || timeout < 1 {
		errs = append(errs, Error{"serving_probes.timeout_seconds", "must be finite and at least 1"})
	}
	if budget := servingProbeOr(p.MaxFirstByteSeconds, timeout); !servingProbeFinite(budget) || budget <= 0 || budget > timeout {
		errs = append(errs, Error{"serving_probes.max_first_byte_seconds", "must be positive and no more than timeout_seconds"})
	}
	if maxAge := servingProbeOr(p.MaxAgeSeconds, 900); !servingProbeFinite(maxAge) || maxAge < 3*interval {
		errs = append(errs, Error{"serving_probes.max_age_seconds", "must be finite and at least three probe intervals"})
	}
	return errs
}

// Policy is one compute node's entry in faas_serving_probe_fleet. Unset
// optional fields are left to the roles' defaults.
func (p ServingProbes) Policy() map[string]any {
	policy := map[string]any{"urls": p.URLs, "expected_body": p.ExpectedBody}
	if p.Interval != "" {
		policy["interval"] = p.Interval
	}
	if p.TimeoutSeconds != 0 {
		policy["timeout_seconds"] = p.TimeoutSeconds
	}
	if p.MaxFirstByteSeconds != 0 {
		policy["max_first_byte_seconds"] = p.MaxFirstByteSeconds
	}
	if p.MaxAgeSeconds != 0 {
		policy["max_age_seconds"] = p.MaxAgeSeconds
	}
	return policy
}

func servingProbeOr(value, fallback float64) float64 {
	if value == 0 {
		return fallback
	}
	return value
}

func servingProbeFinite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
