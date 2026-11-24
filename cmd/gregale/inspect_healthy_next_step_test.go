package main_test

import (
	"bytes"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Prod hunt r27: `app health APP --explain` of a healthy app ended its header
// with "Next step: gregale app health APP", the command just run.
func TestInspectHealthyAppSuggestsNoNextStep(t *testing.T) {
	var summary main.InspectSummary
	summary.App.Slug = "bh29-front"
	var buf bytes.Buffer
	main.RenderInspectSummaryHuman(&buf, summary)
	out := buf.String()
	if !strings.Contains(out, "No urgent issues reported") {
		t.Fatalf("summary is not the healthy state:\n%s", out)
	}
	if strings.Contains(out, "app health bh29-front\n") || strings.Contains(out, "Next step") {
		t.Fatalf("healthy app suggests re-running health:\n%s", out)
	}

	summary.Unavailable = []string{"logs"}
	buf.Reset()
	main.RenderInspectSummaryHuman(&buf, summary)
	if !strings.Contains(buf.String(), "Next step") {
		t.Fatalf("an incomplete summary should keep its next step:\n%s", buf.String())
	}
}
