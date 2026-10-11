package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	profileCheckFailure        = 1
	profileCheckAuthFailure    = 2
	profileCheckNetworkFailure = 3
	profileCheckTimeout        = 124
	profileCheckCancelled      = 130
)

type profileCheckAccount struct {
	ID     string `json:"id"`
	Email  string `json:"email"`
	Plan   string `json:"plan"`
	Status string `json:"status"`
}

type profileCheckDiagnostic struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Hint       string `json:"hint"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

type profileCheckReport struct {
	OK                bool                    `json:"ok"`
	ExitCode          int                     `json:"exit_code"`
	Connection        *connectionContext      `json:"connection"`
	CredentialSource  string                  `json:"credential_source"`
	EndpointReachable *bool                   `json:"endpoint_reachable"`
	Authentication    string                  `json:"authentication"`
	Account           *profileCheckAccount    `json:"account,omitempty"`
	Diagnostic        *profileCheckDiagnostic `json:"diagnostic,omitempty"`
}

func cmdProfileCheck(args []string) int {
	fs := newFlagSet("profile check", flag.ContinueOnError)
	setFlagOutput(fs, osStderr)
	timeout := fs.Duration("timeout", 10*time.Second, "maximum request duration (for example 5s)")
	if err := fs.Parse(args); err != nil {
		return profileCheckFailure
	}
	if fs.NArg() != 0 || *timeout <= 0 {
		PrintUsage(osStderr, "usage: gregale [--profile NAME] profile check [--timeout 10s] (timeout must be positive)", "config")
		return profileCheckFailure
	}
	// Validate before rendering the endpoint: FAAS_API may contain embedded secrets.
	if _, err := validateConfigAPIBase(apiBase()); err != nil {
		return printErr("Invalid API endpoint", errors.New("set api-base to an http(s) URL without credentials, query parameters, or fragments; FAAS_API overrides profile settings"))
	}
	connection := effectiveConnectionContext()
	previousProfile := profileOverride
	profileOverride = connection.Profile
	defer func() { profileOverride = previousProfile }()
	token := loadToken()
	source := "none"
	if token != "" {
		source = "profile:" + connection.Profile
	}
	if os.Getenv("FAAS_TOKEN") != "" {
		source = "env:FAAS_TOKEN"
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, *timeout)
	defer cancel()
	client := NewClient(connection.APIBase, token)
	client.SetCompletionCache(nil)
	client.HTTPClient().Timeout = *timeout
	report := checkProfileConnection(ctx, client, connection, source, token != "")
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		renderProfileCheck(report)
	}
	return report.ExitCode
}

func checkProfileConnection(ctx context.Context, client *Client, connection *connectionContext, source string, hasCredential bool) profileCheckReport {
	report := profileCheckReport{Connection: connection, CredentialSource: source, Authentication: "unknown"}
	if !hasCredential {
		report.Authentication = "missing"
	}
	authHint := "Run gregale --profile " + connection.Profile + " login."
	if source == "env:FAAS_TOKEN" {
		authHint = "Replace or unset FAAS_TOKEN; it overrides this profile's stored credential."
	}
	account, err := client.Whoami(ctx)
	if err == nil {
		reachable := true
		report.EndpointReachable = &reachable
		if !hasCredential {
			report.fail(profileCheckAuthFailure, "credentials_missing", "No credential is configured for this connection.", authHint, 0)
		} else if account.ID == "" && account.Email == "" {
			report.fail(profileCheckFailure, "invalid_response", "The endpoint returned no account identity.", "Check that the configured endpoint is the Gregale API.", 0)
		} else {
			report.OK = true
			report.Authentication = "authenticated"
			report.Account = &profileCheckAccount{ID: account.ID, Email: account.Email, Plan: account.Plan, Status: account.Status}
		}
		return report
	}
	var remote *api.APIError
	if errors.As(err, &remote) {
		reachable := true
		report.EndpointReachable = &reachable
		status := remote.Problem.Status
		switch status {
		case http.StatusUnauthorized:
			code, message := "credentials_rejected", "The API rejected this credential."
			report.Authentication = "rejected"
			if !hasCredential {
				code, message = "credentials_missing", "No credential is configured for this connection."
				report.Authentication = "missing"
			} else if remote.Problem.Code == api.CodeAPIKeyExpired || remote.Problem.Code == api.CodeSessionExpired {
				code, message = "credentials_expired", "The API reports that this credential has expired."
				report.Authentication = "expired"
			} else if remote.Problem.Code == api.CodeAPIKeyRevoked {
				code, message = "credentials_revoked", "The API reports that this credential has been revoked."
				report.Authentication = "revoked"
			}
			report.fail(profileCheckAuthFailure, code, message, authHint, status)
		case http.StatusForbidden:
			report.Authentication = "forbidden"
			report.fail(profileCheckAuthFailure, "access_denied", "The API denied access to the account identity.", "Check the credential's permissions and the selected account.", status)
		default:
			report.fail(profileCheckFailure, "api_error", "The API returned an error.", "Check the API endpoint and server status, then try again.", status)
		}
		return report
	}
	switch {
	case errors.Is(err, context.Canceled):
		report.fail(profileCheckCancelled, "cancelled", "The connection check was interrupted.", "Run the check again when ready.", 0)
	case errors.Is(err, context.DeadlineExceeded):
		report.fail(profileCheckTimeout, "timeout", "The connection check timed out.", "Check connectivity or increase --timeout.", 0)
	default:
		var network net.Error
		var dns *net.DNSError
		var unknownCA x509.UnknownAuthorityError
		var invalidCertificate x509.CertificateInvalidError
		var hostname x509.HostnameError
		var tlsHeader tls.RecordHeaderError
		var syntax *json.SyntaxError
		var typeError *json.UnmarshalTypeError
		switch {
		case errors.As(err, &network) && network.Timeout():
			report.fail(profileCheckTimeout, "timeout", "The connection check timed out.", "Check connectivity or increase --timeout.", 0)
		case errors.As(err, &unknownCA) || errors.As(err, &invalidCertificate) || errors.As(err, &hostname) || errors.As(err, &tlsHeader):
			unreachable := false
			report.EndpointReachable = &unreachable
			report.fail(profileCheckNetworkFailure, "tls_failed", "The TLS connection could not be verified or established.", "Check the endpoint's HTTPS configuration and trusted certificates.", 0)
		case errors.As(err, &dns):
			unreachable := false
			report.EndpointReachable = &unreachable
			report.fail(profileCheckNetworkFailure, "dns_failed", "The API hostname could not be resolved.", "Check the endpoint hostname and DNS connectivity.", 0)
		case errors.As(err, &network):
			unreachable := false
			report.EndpointReachable = &unreachable
			report.fail(profileCheckNetworkFailure, "connection_failed", "The API connection failed.", "Check the endpoint, network connectivity, and firewall settings.", 0)
		case errors.As(err, &syntax) || errors.As(err, &typeError):
			reachable := true
			report.EndpointReachable = &reachable
			report.fail(profileCheckFailure, "invalid_response", "The endpoint returned an invalid account response.", "Check that the configured endpoint is the Gregale API.", 0)
		default:
			report.fail(profileCheckFailure, "request_failed", "The account request could not be completed.", "Check the endpoint and server status, then try again.", 0)
		}
	}
	return report
}

func (r *profileCheckReport) fail(exit int, code, message, hint string, status int) {
	r.ExitCode = exit
	r.Diagnostic = &profileCheckDiagnostic{Code: code, Message: message, Hint: hint, HTTPStatus: status}
}

func renderProfileCheck(report profileCheckReport) {
	c := report.Connection
	_, _ = fmt.Fprintf(osStdout, "Profile: %s (source=%s)\nAPI: %s (source=%s)\nCredential source: %s\n", c.Profile, c.ProfileSource, c.APIBase, c.APISource, report.CredentialSource)
	if c.APISource == "env:FAAS_API" {
		_, _ = fmt.Fprintln(osStdout, "FAAS_API overrides this profile's saved endpoint.")
	}
	if report.CredentialSource == "env:FAAS_TOKEN" {
		_, _ = fmt.Fprintln(osStdout, "FAAS_TOKEN overrides this profile's stored credential.")
	}
	if report.OK {
		a := report.Account
		PrintOK(osStdout, "Connected as %s (id=%s, plan=%s, status=%s).", a.Email, a.ID, a.Plan, a.Status)
		return
	}
	d := report.Diagnostic
	_, _ = fmt.Fprintf(osStdout, "Check failed [%s]: %s\n", d.Code, d.Message)
	if d.HTTPStatus != 0 {
		_, _ = fmt.Fprintf(osStdout, "HTTP status: %d\n", d.HTTPStatus)
	}
	_, _ = fmt.Fprintf(osStdout, "Next: %s\n", d.Hint)
}
