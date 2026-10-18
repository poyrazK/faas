// ADR-1655: profile commands preserve explicit immutable execution identity.
package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/ci"
)

func TestCIProfileRequestsAndSubmissionRejectOverrides(t *testing.T) {
	id := uuid.NewString()
	req := ci.ExecutionProfileRequest{RevisionID: uuid.NewString(), BindingID: uuid.NewString(), Templates: map[string]string{"test": uuid.NewString()}}
	method, path, body, err := ciProfileRequest("put", "checks", "", req)
	if err != nil || method != http.MethodPut || path != "/v1/internal/ci/profiles/checks" || body.(ci.ExecutionProfileRequest).BindingID != req.BindingID {
		t.Fatal("profile PUT lost its immutable inputs")
	}
	if _, _, _, err := ciProfileRequest("put", "../outside", "", req); err == nil {
		t.Fatal("invalid profile name accepted")
	}
	if method, path, _, err := ciProfileRequest("disable", "", id, req); err != nil || method != http.MethodDelete || path != "/v1/internal/ci/profiles/"+id {
		t.Fatal("profile disable request malformed")
	}
	if _, _, _, err := ciProfileRequest("status", "", "checks", req); err == nil {
		t.Fatal("profile lookup accepted an ambiguous name")
	}
	o := ciExecutionOptions{profile: id, commit: strings.Repeat("a", 40), key: "profile-admission"}
	method, _, body, headers, err := ciExecutionRequest("submit", o)
	if err != nil || method != http.MethodPost || body.(ci.JobsSubmitRequest).ProfileID != id || headers.Get("Idempotency-Key") != o.key {
		t.Fatal("profile execution lost its request identity")
	}
	for _, name := range []string{"revision", "binding", "template", "key", "commit"} {
		changed := o
		switch name {
		case "revision":
			changed.revision = req.RevisionID
		case "binding":
			changed.binding = req.BindingID
		case "template":
			changed.templates = req.Templates
		case "key":
			changed.key = ""
		case "commit":
			changed.commit = "main"
		}
		if _, _, _, _, err := ciExecutionRequest("submit", changed); err == nil {
			t.Fatalf("invalid profile submission accepted: %s", name)
		}
	}
}
