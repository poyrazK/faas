// adr: 20261112223000

package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestAppSettingFlagsSetTheRequest(t *testing.T) {
	dir := t.TempDir()
	robots := filepath.Join(dir, "robots.txt")
	icon := filepath.Join(dir, "favicon.ico")
	if err := os.WriteFile(robots, []byte("User-agent: *\nDisallow: /admin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(icon, []byte{0, 0, 1, 0}, 0o600); err != nil {
		t.Fatal(err)
	}
	req, err := main.ParseAppSettingFlags([]string{
		"--request-rate-limit-rps", "50", "--request-rate-limit-burst", "0",
		"--robots-txt-file", robots, "--favicon-file", icon,
		"--no-session-affinity", "--version-affinity-cookie", " release ",
		"--version-affinity-managed-cookie", "--cors-default", "--cors-default-origins", "https://a.example, *.b.example ,",
	})
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case req.RequestRateLimitRPS == nil || *req.RequestRateLimitRPS != 50:
		t.Errorf("rps = %v", req.RequestRateLimitRPS)
	case req.RequestRateLimitBurst == nil || *req.RequestRateLimitBurst != 0:
		t.Errorf("burst = %v; 0 must be sent to restore the plan default", req.RequestRateLimitBurst)
	case req.RobotsTxt == nil || !strings.Contains(*req.RobotsTxt, "Disallow: /admin"):
		t.Errorf("robots = %v", req.RobotsTxt)
	case req.Favicon == nil || len(*req.Favicon) != 4:
		t.Errorf("favicon = %v", req.Favicon)
	case req.SessionAffinity == nil || *req.SessionAffinity:
		t.Errorf("session affinity = %v, want false", req.SessionAffinity)
	case req.VersionAffinityCookie == nil || *req.VersionAffinityCookie != "release":
		t.Errorf("version cookie = %v", req.VersionAffinityCookie)
	case req.VersionAffinityManagedCookie == nil || !*req.VersionAffinityManagedCookie:
		t.Errorf("managed cookie = %v", req.VersionAffinityManagedCookie)
	case req.CORSDefaultEnabled == nil || !*req.CORSDefaultEnabled:
		t.Errorf("cors default = %v", req.CORSDefaultEnabled)
	case req.CORSDefaultOrigins == nil || strings.Join(*req.CORSDefaultOrigins, "|") != "https://a.example|*.b.example":
		t.Errorf("cors origins = %v", req.CORSDefaultOrigins)
	}
}

func TestAppSettingFlagsClearAndRefuse(t *testing.T) {
	req, err := main.ParseAppSettingFlags([]string{"--default-robots-txt", "--no-favicon", "--cors-default-origins", ""})
	if err != nil {
		t.Fatal(err)
	}
	if req.RobotsTxt == nil || *req.RobotsTxt != "" || req.Favicon == nil || len(*req.Favicon) != 0 ||
		req.CORSDefaultOrigins == nil || len(*req.CORSDefaultOrigins) != 0 {
		t.Fatalf("clearing flags did not send empty values: %+v", req)
	}
	// Nothing passed leaves every field nil, so the command reads instead.
	if req, _ := main.ParseAppSettingFlags(nil); req.SessionAffinity != nil || req.RobotsTxt != nil || req.RequestRateLimitRPS != nil {
		t.Fatalf("no flags set fields: %+v", req)
	}
	for _, args := range [][]string{
		{"--session-affinity", "--no-session-affinity"},
		{"--favicon-file", "x", "--no-favicon"},
		{"--request-rate-limit-rps", "-1"},
		{"--robots-txt-file", filepath.Join(t.TempDir(), "missing")},
	} {
		if _, err := main.ParseAppSettingFlags(args); err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
}
