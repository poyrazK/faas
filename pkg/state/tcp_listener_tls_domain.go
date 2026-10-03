package state

import "errors"

// ValidateTCPListenerTLSDomain requires a verified app-wide domain. Environment
// routes need their own admission scope and cannot use an app-wide TCP endpoint.
// Certificate readiness is checked separately by the public-edge provider.
func ValidateTCPListenerTLSDomain(appID, hostname string, domain CustomDomain) error {
	if appID == "" || hostname == "" || domain.Domain != hostname || domain.AppID != appID || !domain.Verified() || domain.EnvironmentID != "" {
		return errors.New("TCP TLS hostname must be a verified app-wide domain owned by the listener app")
	}
	return nil
}
