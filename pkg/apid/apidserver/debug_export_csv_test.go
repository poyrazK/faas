package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 127 — amendment 2026-11-05: an exported CSV never hands a spreadsheet
// a formula taken from the app's traffic.
func TestCSVSafeCell(t *testing.T) {
	tests := map[string]string{
		"GET /checkout":        "GET /checkout",
		"=HYPERLINK(\"x\")":    "'=HYPERLINK(\"x\")",
		"+1":                   "'+1",
		"-cmd":                 "'-cmd",
		"@SUM(A1)":             "'@SUM(A1)",
		"\tx":                  "'\tx",
		"\rx":                  "'\rx",
		"":                     "",
		"200":                  "200",
		"TypeError: x is null": "TypeError: x is null",
	}
	for in, want := range tests {
		if got := apidserver.CSVSafeCell(in); got != want {
			t.Errorf("CSVSafeCell(%q) = %q, want %q", in, got, want)
		}
	}
}
