package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

type startCompatibilityError struct {
	retryable bool
	message   string
}

func (e *startCompatibilityError) Error() string { return e.message }

func startUnsupportedVerification() error {
	return &startCompatibilityError{message: "This CLI and Gregale installation have incompatible deployment verification support. Check for updates to the CLI and installation, or contact its operator, then run gregale start again. Saved launches are preserved."}
}

func startUnavailableVerification() error {
	return &startCompatibilityError{retryable: true, message: "Deployment verification is temporarily unavailable. Retry when the service recovers; an accepted deployment can be continued with gregale start."}
}

// Always query the reviewed connection again before mutation. Compatibility
// metadata is ephemeral and must not become a stale permission to deploy.
func (r *startRunner) requireStartCompatibility(ctx context.Context, client *Client) error {
	readCtx, cancel := context.WithTimeout(ctx, api.DeploymentResponseCheckPreflightTimeout)
	defer cancel()
	capabilities, err := client.GetDeploymentResponseCheckCapabilities(readCtx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return startVerificationServiceError(err)
	}
	if capabilities.SupportsCurrentProtocol() {
		return nil
	}
	if capabilities.ProtocolVersion != api.DeploymentResponseCheckProtocolVersion || capabilities.UnavailableReason == api.DeploymentResponseCheckGatewayUnsupported || capabilities.UnavailableReason == api.DeploymentResponseCheckGatewayUnconfigured {
		return startUnsupportedVerification()
	}
	return startUnavailableVerification()
}

func startVerificationServiceError(err error) error {
	var problem *APIError
	if errors.As(err, &problem) {
		switch problem.Problem.Status {
		case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
			return startUnsupportedVerification()
		case http.StatusUnauthorized, http.StatusForbidden:
			return &startCompatibilityError{message: "Access to deployment verification was denied. Sign in to the reviewed account with deployment-write access, then run gregale start again. Saved launches are preserved."}
		}
	}
	return startUnavailableVerification()
}
