package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestPrintDoctorReportMarksStatusWithoutGlyphs — piped `domains doctor`
// output dropped the ✓/✗ glyphs, so a failing check read like a passing
// one, and a CNAME fix shared by two checks was listed twice.
func TestPrintDoctorReportMarksStatusWithoutGlyphs(t *testing.T) {
	resetJSONOut(t)
	tty := false
	prev := testOnlyTTY
	testOnlyTTY = &tty
	t.Cleanup(func() { testOnlyTTY = prev })
	fix := "Set CNAME h3.example.com → gregale.dev"
	var out bytes.Buffer
	printDoctorReport(&out, api.DomainDoctorReport{Domain: "h3.example.com", Checks: []api.DomainDoctorCheck{
		{Name: "dns_record", Status: doctorCheckFail, Detail: "no DNS record resolves at h3.example.com", Remediation: fix},
		{Name: "points_to_gregale", Status: doctorCheckFail, Detail: "no Gregale CNAME target was observed", Remediation: fix},
		{Name: "caa_permits", Status: doctorCheckOK, Detail: "CAA permits certificate issuance"},
		{Name: "tls_certificate", Status: doctorCheckPend, Detail: "cert engine has not yet issued"},
	}})
	got := out.String()
	for _, want := range []string{"[fail]    dns_record", "[ok]      caa_permits", "[pending] tls_certificate"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "  - "+fix); n != 1 {
		t.Fatalf("shared fix listed %d times:\n%s", n, got)
	}
}
