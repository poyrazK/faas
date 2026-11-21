package reposcan

import (
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// composeHTTPReadiness turns a Compose healthcheck that only fetches a local
// URL with curl or wget (`curl -f http://localhost:8080/actuator/health ||
// exit 1`) into an HTTP readiness check the platform runs from the host. The
// command form needs curl or wget inside the image, so Compose users added
// them to distroless and slim images only for this (retail-store feedback B2,
// 2026-11-17). The converted service gets Test ["NONE"] in place of the
// command, which also disables an inherited image HEALTHCHECK running the
// same fetch. Anything else (another port, a header, a POST, a shell
// pipeline) stays a command check.
func composeHTTPReadiness(check *api.ComposeHealthcheck, servingPort int) (*api.DeploymentHealthcheck, *api.ComposeHealthcheck, bool) {
	if check == nil || servingPort <= 0 {
		return nil, check, false
	}
	argv, ok := composeHealthcheckArgv(check.Test)
	if !ok {
		return nil, check, false
	}
	target, ok := localFetchURL(argv)
	if !ok {
		return nil, check, false
	}
	port := 80
	if p := target.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, check, false
		}
		port = n
	}
	if port != servingPort {
		return nil, check, false
	}
	probePath := target.EscapedPath()
	if probePath == "" {
		probePath = "/"
	}
	if target.RawQuery != "" {
		probePath += "?" + target.RawQuery
	}
	probe := &api.DeploymentHealthcheck{
		Path:         probePath,
		IntervalS:    durationSecondsNS(check.IntervalNS),
		TimeoutS:     durationSecondsNS(check.TimeoutNS),
		Retries:      check.Retries,
		StartPeriodS: durationSecondsNS(check.StartPeriodNS),
	}
	if problem := (&api.CreateDeploymentOverrides{Healthcheck: probe}).Validate(api.Limits{}); problem != nil {
		return nil, check, false
	}
	disabled := &api.ComposeHealthcheck{Test: []string{"NONE"}}
	return probe, disabled, true
}

func durationSecondsNS(ns int64) int {
	if ns <= 0 {
		return 0
	}
	return ceilSeconds(time.Duration(ns))
}

// composeHealthcheckArgv returns the command a CMD or CMD-SHELL test runs.
// A shell test is accepted only as one plain command, optionally followed by
// `|| exit 1`: no other operators, expansions or quoting.
func composeHealthcheckArgv(test []string) ([]string, bool) {
	if len(test) < 2 {
		return nil, false
	}
	switch test[0] {
	case "CMD":
		return test[1:], true
	case "CMD-SHELL":
		if len(test) != 2 {
			return nil, false
		}
		line := strings.TrimSpace(test[1])
		for _, tail := range []string{"|| exit 1", "||exit 1", "|| false"} {
			if trimmed, ok := strings.CutSuffix(line, tail); ok {
				line = strings.TrimSpace(trimmed)
				break
			}
		}
		if strings.ContainsAny(line, "|&;<>$`'\"\\(){}*\n") {
			return nil, false
		}
		return strings.Fields(line), true
	}
	return nil, false
}

// fetchFlagsWithValue are curl and wget options that take the next word.
var fetchFlagsWithValue = map[string]bool{
	"-o": true, "--output": true, "-O": true, "--output-document": true,
	"-m": true, "--max-time": true, "--connect-timeout": true, "--retry": true,
	"-w": true, "--write-out": true, "-T": true, "--timeout": true, "-t": true, "--tries": true,
}

// fetchFlagsChangingRequest change what is fetched or how, so the host-side
// GET would not be the same check.
var fetchFlagsChangingRequest = []string{
	"-X", "--request", "-H", "--header", "-d", "--data", "-u", "--user", "-b", "--cookie",
	"--post-data", "--post-file", "--method", "--body-data", "-A", "--user-agent", "--unix-socket",
	"-k", "--insecure", "--no-check-certificate", "-I", "--head", "-F", "--form", "--resolve",
}

// localFetchURL reads `curl|wget [flags] URL` and returns the URL when it is
// a plain http GET of this instance (localhost, 127.0.0.1, 0.0.0.0 or ::1).
func localFetchURL(argv []string) (*url.URL, bool) {
	if len(argv) < 2 {
		return nil, false
	}
	switch path.Base(argv[0]) {
	case "curl", "wget":
	default:
		return nil, false
	}
	var target string
	for i := 1; i < len(argv); i++ {
		word := argv[i]
		if !strings.HasPrefix(word, "-") {
			if target != "" {
				return nil, false
			}
			target = word
			continue
		}
		name, _, hasValue := strings.Cut(word, "=")
		for _, bad := range fetchFlagsChangingRequest {
			if name == bad || (len(bad) == 2 && strings.HasPrefix(word, bad) && !strings.HasPrefix(word, "--")) {
				return nil, false
			}
		}
		if fetchFlagsWithValue[name] && !hasValue {
			i++
		}
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Fragment != "" {
		return nil, false
	}
	switch parsed.Hostname() {
	case "localhost", "127.0.0.1", "0.0.0.0", "::1":
		return parsed, true
	}
	return nil, false
}

// composePublicAuth validates x-gregale-public-auth for one service, taking
// the file's top-level value when the service sets none.
func composePublicAuth(service, top string) (string, error) {
	value := strings.TrimSpace(service)
	if value == "" {
		value = strings.TrimSpace(top)
	}
	switch value {
	case "", api.AppPublicAuthModeOpen, api.AppPublicAuthModeBearer:
		return value, nil
	}
	return "", fmt.Errorf("x-gregale-public-auth %q must be open or bearer", value)
}
