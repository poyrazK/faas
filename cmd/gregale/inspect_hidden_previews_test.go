package main_test

import (
	"errors"
	"net/http"
	"slices"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// rc.295 hides unreleased preview capabilities exactly like missing routes
// (ADR-20261023115741). inspect read the hidden openapi, operational and
// upstreams sections as read failures, so every app's headline said
// "Incomplete information … Current health is not established" (2026-10-26).
func TestInspectHiddenPreviewsAreNotReadFailures(t *testing.T) {
	hidden := &main.APIError{Problem: api.Problem{Status: http.StatusNotFound, Code: api.CodeNotFound, Detail: "no such API route"}}
	missingRow := &main.APIError{Problem: api.Problem{Status: http.StatusNotFound, Code: api.CodeNotFound, Detail: "no deployment"}}
	in := main.InspectSummaryInputs{OpenAPIErr: hidden, OperationalErr: hidden, UpstreamsErr: hidden, AlertsErr: missingRow, DeploymentErr: errors.New("connection reset")}
	if got := main.InspectUnavailable(in); !slices.Equal(got, []string{"deployment", "alerts"}) {
		t.Fatalf("unavailable = %v, want only the real read failures", got)
	}
	if got := main.InspectUnavailable(main.InspectSummaryInputs{OpenAPIErr: hidden, OperationalErr: hidden, UpstreamsErr: hidden}); len(got) != 0 {
		t.Fatalf("unavailable = %v, want none for hidden previews", got)
	}
}
