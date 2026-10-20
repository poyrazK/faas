package main_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestPrintDoctorReportMarksStatusWithoutGlyphs — piped `domains doctor`
// output dropped the ✓/✗ glyphs, so a failing check read like a passing
// one, and a CNAME fix shared by two checks was listed twice.
func TestPrintDoctorReportMarksStatusWithoutGlyphs(t *testing.T) {
	main.ResetJSONOut(t)
	tty := false
	prev := (*main.TestOnlyTTY)
	(*main.TestOnlyTTY) = &tty
	t.Cleanup(func() { (*main.TestOnlyTTY) = prev })
	fix := "Set CNAME h3.example.com → gregale.dev"
	var out bytes.Buffer
	main.PrintDoctorReport(&out, api.DomainDoctorReport{Domain: "h3.example.com", Checks: []api.DomainDoctorCheck{
		{Name: "dns_record", Status: main.DoctorCheckFail, Detail: "no DNS record resolves at h3.example.com", Remediation: fix},
		{Name: "points_to_gregale", Status: main.DoctorCheckFail, Detail: "no Gregale CNAME target was observed", Remediation: fix},
		{Name: "caa_permits", Status: main.DoctorCheckOK, Detail: "CAA permits certificate issuance"},
		{Name: "tls_certificate", Status: main.DoctorCheckPend, Detail: "cert engine has not yet issued"},
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
