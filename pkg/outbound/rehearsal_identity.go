// adr: 20261017201006
package outbound

import (
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type RehearsalRelayIdentityVerifier interface {
	VerifyRehearsalRelay(string) (WorkloadIdentity, string, error)
}

func (v *WorkloadIdentityVerifier) VerifyRehearsalRelay(raw string) (WorkloadIdentity, string, error) {
	if len(raw) == 0 || len(raw) > maxIdentityTokenBytes {
		return WorkloadIdentity{}, "", ErrInvalidWorkloadIdentity
	}
	token, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		return WorkloadIdentity{}, "", ErrInvalidWorkloadIdentity
	}
	// The untrusted audience selects only a bounded run identifier; the ordinary
	// signature, issuer, subject, expiry and exact audience checks follow below.
	var claims jwt.Claims
	if err = token.UnsafeClaimsWithoutVerification(&claims); err != nil || len(claims.Audience) != 1 {
		return WorkloadIdentity{}, "", ErrInvalidWorkloadIdentity
	}
	run, ok := workloadidentity.RehearsalRelayRun(claims.Audience[0])
	if !ok {
		return WorkloadIdentity{}, "", ErrInvalidWorkloadIdentity
	}
	identity, err := v.verifyAppAudience(raw, claims.Audience[0])
	if err != nil {
		return WorkloadIdentity{}, "", err
	}
	return identity, run, nil
}
