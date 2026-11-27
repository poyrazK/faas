package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// prod hunt 2026-11-21: a revoked key printed "the [REDACTED] has been
// revoked; mint a new one via POST /v1/keys": the secret redactor read
// "bearer key" as a bearer token. The details survive redaction and name
// the CLI command.
func TestAPIKeyProblemsSurviveRedaction(t *testing.T) {
	for _, p := range []*api.Problem{api.ErrAPIKeyRevoked(), api.ErrAPIKeyExpired()} {
		got := main.DiagnosticProblem(*p)
		if strings.Contains(got.Detail, "REDACTED") || !strings.Contains(got.Detail, "gregale key create") {
			t.Errorf("%s detail = %q", p.Code, got.Detail)
		}
	}
}
