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

// verificationFailure classifies why the deployment response check cannot run,
// so each caller (gregale start, gregale release verify) can phrase its own
// recovery while sharing one decision about what is permanent.
type verificationFailure int

const (
	verificationUnavailable verificationFailure = iota // transient; retrying can help
	verificationUnsupported                            // CLI/installation mismatch; retrying never helps
	verificationDenied                                 // the account lacks deployment-write access
)

func startUnsupportedVerification() error {
	return startVerificationError(verificationUnsupported)
}

func startVerificationError(failure verificationFailure) error {
	switch failure {
	case verificationUnsupported:
		return &startCompatibilityError{message: "This CLI and Gregale installation have incompatible deployment verification support. Check for updates to the CLI and installation, or contact its operator, then run gregale start again. Saved launches are preserved."}
	case verificationDenied:
		return &startCompatibilityError{message: "Access to deployment verification was denied. Sign in to the reviewed account with deployment-write access, then run gregale start again. Saved launches are preserved."}
	default:
		return &startCompatibilityError{retryable: true, message: "Deployment verification is temporarily unavailable. Retry when the service recovers; an accepted deployment can be continued with gregale start."}
	}
}

func releaseVerifyError(failure verificationFailure) error {
	switch failure {
	case verificationUnsupported:
		return errors.New("this CLI and Gregale installation have incompatible deployment verification support; update the CLI or ask the installation's operator. The deployment is unchanged")
	case verificationDenied:
		return errors.New("access to deployment verification was denied; sign in with an account that has deployment-write access. The deployment is unchanged")
	default:
		return errors.New("deployment verification is temporarily unavailable; retry when the service recovers. The deployment is unchanged")
	}
}

// checkVerificationSupport reads the installation's response-check capability.
// It returns ok=false with a classified failure, or a context error.
func checkVerificationSupport(ctx context.Context, client *Client) (bool, verificationFailure, error) {
	readCtx, cancel := context.WithTimeout(ctx, api.DeploymentResponseCheckPreflightTimeout)
	defer cancel()
	capabilities, err := client.GetDeploymentResponseCheckCapabilities(readCtx)
	if ctx.Err() != nil {
		return false, 0, ctx.Err()
	}
	if err != nil {
		return false, classifyVerificationServiceError(err), nil
	}
	if capabilities.SupportsCurrentProtocol() {
		return true, 0, nil
	}
	if capabilities.ProtocolVersion != api.DeploymentResponseCheckProtocolVersion || capabilities.UnavailableReason == api.DeploymentResponseCheckGatewayUnsupported || capabilities.UnavailableReason == api.DeploymentResponseCheckGatewayUnconfigured {
		return false, verificationUnsupported, nil
	}
	return false, verificationUnavailable, nil
}

// Always query the reviewed connection again before mutation. Compatibility
// metadata is ephemeral and must not become a stale permission to deploy.
func (r *startRunner) requireStartCompatibility(ctx context.Context, client *Client) error {
	ok, failure, err := checkVerificationSupport(ctx, client)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	return startVerificationError(failure)
}

func classifyVerificationServiceError(err error) verificationFailure {
	var problem *APIError
	if errors.As(err, &problem) {
		switch problem.Problem.Status {
		case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
			return verificationUnsupported
		case http.StatusUnauthorized, http.StatusForbidden:
			return verificationDenied
		}
	}
	return verificationUnavailable
}

func startVerificationServiceError(err error) error {
	return startVerificationError(classifyVerificationServiceError(err))
}
