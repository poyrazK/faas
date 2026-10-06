package deploydiff

import (
	"bytes"
	"strings"
	"testing"
)

// Prod hunt #3: `deploy --diff` printed "deployment.frameworknode → express";
// a field longer than the 14-column label ran into the value.
func TestPrintScalar_LongFieldKeepsSeparator(t *testing.T) {
	ch := Change{Field: "deployment.framework", Kind: ChangeModify, Before: AsAny("node"), After: AsAny("express")}
	var buf bytes.Buffer
	printScalar(&buf, ch)
	if !strings.Contains(buf.String(), "deployment.framework node → express") {
		t.Fatalf("printScalar = %q, want the field separated from the value", buf.String())
	}
}
