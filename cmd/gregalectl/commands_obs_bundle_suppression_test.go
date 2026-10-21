package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestObsBundleSuppressionAndHumanEvidence(t *testing.T) {
	now := time.Now().UTC()
	yes, no := true, false
	e := api.ObsIncidentEvidenceResponse{Incident: api.ObsIncident{Type: "platform_alert"}, Suppression: &api.ObsAlertSuppression{State: "suppressed", CheckedAt: now, UpdatedAt: &now, Silenced: &yes, Inhibited: &no}}
	if !validObsBundleSuppression(e) {
		t.Fatal("valid suppression rejected")
	}
	var out bytes.Buffer
	writeObsEvidenceHuman(&out, e)
	if !strings.Contains(out.String(), "silenced=true inhibited=false") {
		t.Fatalf("human suppression: %s", &out)
	}
	e.Suppression.State = "unavailable"
	if validObsBundleSuppression(e) {
		t.Fatal("unavailable source supplied usable booleans")
	}
	e.Suppression.Silenced, e.Suppression.Inhibited = nil, nil
	if !validObsBundleSuppression(e) {
		t.Fatal("unknown suppression rejected")
	}
	out.Reset()
	writeObsEvidenceHuman(&out, e)
	if !strings.Contains(out.String(), "silenced=unknown inhibited=unknown") {
		t.Fatalf("human unknowns: %s", &out)
	}
	e.Suppression.State = "active"
	e.Suppression.Silenced, e.Suppression.Inhibited = &yes, &no
	if validObsBundleSuppression(e) {
		t.Fatal("inconsistent active state accepted")
	}
	e.Suppression = nil
	if !validObsBundleSuppression(e) {
		t.Fatal("legacy server rejected")
	}
}
