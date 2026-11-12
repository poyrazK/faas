package outboundai

import "testing"

// ADR 20261027132936 amendment 2: caller-chosen CSV cells never start a
// spreadsheet formula.
func TestCSVTextNeutralizesFormulas(t *testing.T) {
	for in, want := range map[string]string{
		"=HYPERLINK(\"x\")": "'=HYPERLINK(\"x\")",
		"+1":                "'+1",
		"-2":                "'-2",
		"@SUM(A1)":          "'@SUM(A1)",
		"user_1":            "user_1",
		"team=search":       "team=search",
		"":                  "",
	} {
		if got := csvText(in); got != want {
			t.Errorf("csvText(%q) = %q, want %q", in, got, want)
		}
	}
}
