package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func renderAppHealthVerification(w io.Writer, slug string, health api.AppHealthResponse) {
	message := "Current health is not established by this assessment."
	switch health.Status {
	case "healthy":
		message = "The assessment reports healthy HTTP evidence for the scope shown above; its timestamp and validity limit that result."
	case "degraded", "unhealthy":
		message = "The reported HTTP health evidence needs attention; review the findings below."
	}
	_, _ = fmt.Fprintln(w, message)
	if len(health.ServingDeploymentIDs) > 0 {
		_, _ = fmt.Fprintf(w, "Serving deployments assessed: %s\n", compactInspectFailureText(strings.Join(health.ServingDeploymentIDs, ", ")))
	}
	if health.MetricsAsOf != "" {
		_, _ = fmt.Fprintf(w, "Request evidence as of: %s\n", compactInspectFailureText(health.MetricsAsOf))
	}
	_, _ = fmt.Fprintln(w, "This read uses recorded evidence and does not wake the app or prove every route and workload is healthy.")
	if health.Status != "healthy" {
		_, _ = fmt.Fprintf(w, "Next investigation (POSIX shells):\n  %s inspect %s\n", recoveryCLI(), recoveryArgument(slug))
	}
}
