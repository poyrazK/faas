package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Production-us rc.294: a deploy refused by a freeze said "Resume cannot
// submit a deployment" and then printed "Resume: gregale deploy resume …",
// which only answers that the submission stopped before acceptance (Hunt 14
// H14-10).
func TestRejectedDeployOffersNoResume(t *testing.T) {
	resume, hint := main.SavedDeployRecovery("rejected")
	if resume != "" {
		t.Errorf("rejected deploy offers %q; resume refuses a rejected submission", resume)
	}
	if strings.Contains(strings.ToLower(hint), "resume") {
		t.Errorf("hint %q mentions resume", hint)
	}
	if resume, _ := main.SavedDeployRecovery("accepted"); resume == "" {
		t.Error("accepted deploy lost its resume command")
	}
}
