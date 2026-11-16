// adr: 20261112223000

package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// appSettingFlags are `gregale app <slug>` flags for app settings that were
// API-only (docs/cli-api-parity-exemptions.txt, ADR-20261112223000 §3).
type appSettingFlags struct {
	rateRPS, rateBurst             *int
	robotsFile                     *string
	defaultRobots                  *bool
	faviconFile                    *string
	noFavicon                      *bool
	sessionAffinity, noSessionAff  *bool
	versionCookie                  *string
	managedCookie, noManagedCookie *bool
	corsDefault, noCorsDefault     *bool
	corsOrigins                    *string
}

// maxAppSettingFileBytes bounds --robots-txt-file and --favicon-file; the
// server enforces its own, smaller limits.
const maxAppSettingFileBytes = 1 << 20

func registerAppSettingFlags(fs *flag.FlagSet) *appSettingFlags {
	return &appSettingFlags{
		rateRPS:         fs.Int("request-rate-limit-rps", 0, "app-wide request rate limit per second; 0 restores the plan default"),
		rateBurst:       fs.Int("request-rate-limit-burst", 0, "app-wide request burst; 0 restores the plan default"),
		robotsFile:      fs.String("robots-txt-file", "", "serve this file as /robots.txt at the edge"),
		defaultRobots:   fs.Bool("default-robots-txt", false, "restore the platform's allow-all /robots.txt"),
		faviconFile:     fs.String("favicon-file", "", "serve this icon as /favicon.ico at the edge"),
		noFavicon:       fs.Bool("no-favicon", false, "remove the app's edge favicon"),
		sessionAffinity: fs.Bool("session-affinity", false, "route a client to the same running instance (best effort)"),
		noSessionAff:    fs.Bool("no-session-affinity", false, "turn session affinity off"),
		versionCookie:   fs.String("version-affinity-cookie", "", "cookie that pins a client to a release; empty turns it off"),
		managedCookie:   fs.Bool("version-affinity-managed-cookie", false, "let Gregale set the version-affinity cookie"),
		noManagedCookie: fs.Bool("no-version-affinity-managed-cookie", false, "stop Gregale setting the version-affinity cookie"),
		corsDefault:     fs.Bool("cors-default", false, "answer CORS for --cors-default-origins when no CORS edge rule matches"),
		noCorsDefault:   fs.Bool("no-cors-default", false, "turn the default CORS answer off"),
		corsOrigins:     fs.String("cors-default-origins", "", "comma-separated origins for the default CORS answer; empty clears"),
	}
}

// apply sets the request fields of the flags the user passed.
func (f *appSettingFlags) apply(explicit map[string]bool, req *api.UpdateAppRequest) error {
	for _, pair := range [][2]string{{"default-robots-txt", "robots-txt-file"}, {"no-favicon", "favicon-file"},
		{"session-affinity", "no-session-affinity"}, {"version-affinity-managed-cookie", "no-version-affinity-managed-cookie"},
		{"cors-default", "no-cors-default"}} {
		if explicit[pair[0]] && explicit[pair[1]] {
			return fmt.Errorf("--%s and --%s are mutually exclusive", pair[0], pair[1])
		}
	}
	if explicit["request-rate-limit-rps"] {
		if *f.rateRPS < 0 {
			return fmt.Errorf("--request-rate-limit-rps must be 0 or positive")
		}
		req.RequestRateLimitRPS = f.rateRPS
	}
	if explicit["request-rate-limit-burst"] {
		if *f.rateBurst < 0 {
			return fmt.Errorf("--request-rate-limit-burst must be 0 or positive")
		}
		req.RequestRateLimitBurst = f.rateBurst
	}
	if err := f.applyFiles(explicit, req); err != nil {
		return err
	}
	setBool := func(on, off string, field **bool) {
		if explicit[on] || explicit[off] {
			v := explicit[on]
			*field = &v
		}
	}
	setBool("session-affinity", "no-session-affinity", &req.SessionAffinity)
	setBool("version-affinity-managed-cookie", "no-version-affinity-managed-cookie", &req.VersionAffinityManagedCookie)
	setBool("cors-default", "no-cors-default", &req.CORSDefaultEnabled)
	if explicit["version-affinity-cookie"] {
		v := strings.TrimSpace(*f.versionCookie)
		req.VersionAffinityCookie = &v
	}
	if explicit["cors-default-origins"] {
		origins := []string{}
		for _, origin := range strings.Split(*f.corsOrigins, ",") {
			if origin = strings.TrimSpace(origin); origin != "" {
				origins = append(origins, origin)
			}
		}
		req.CORSDefaultOrigins = &origins
	}
	return nil
}

func (f *appSettingFlags) applyFiles(explicit map[string]bool, req *api.UpdateAppRequest) error {
	if explicit["robots-txt-file"] {
		body, err := readAppSettingFile(*f.robotsFile)
		if err != nil {
			return fmt.Errorf("--robots-txt-file: %w", err)
		}
		v := string(body)
		req.RobotsTxt = &v
	}
	if explicit["default-robots-txt"] {
		v := ""
		req.RobotsTxt = &v
	}
	if explicit["favicon-file"] {
		body, err := readAppSettingFile(*f.faviconFile)
		if err != nil {
			return fmt.Errorf("--favicon-file: %w", err)
		}
		req.Favicon = &body
	}
	if explicit["no-favicon"] {
		empty := []byte{}
		req.Favicon = &empty
	}
	return nil
}

func readAppSettingFile(path string) ([]byte, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, maxAppSettingFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxAppSettingFileBytes {
		return nil, fmt.Errorf("file is larger than %d bytes", maxAppSettingFileBytes)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("file is empty")
	}
	return body, nil
}
