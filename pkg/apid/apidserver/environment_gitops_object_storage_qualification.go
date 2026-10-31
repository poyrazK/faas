// adr:20261019121546 — control-plane credential receipts for enforced candidates' buckets.
package apidserver

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/bindingprobe"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

var (
	errCandidateObjectStorageUnavailable = errors.New("candidate object storage credential cannot be opened")
	errCandidateObjectStorageChanged     = errors.New("candidate object storage delivery does not match its binding")
	errCandidateObjectStorageProbe       = errors.New("candidate object storage canary did not pass")
)

type objectStorageCanary func(ctx context.Context, prefix, mode string, getenv func(string) string) api.ObjectStorageBindingProbeReport

// verifyCandidateCredentials records receipts for the graph's unverified
// managed PostgreSQL and object storage deliveries before activation.
func (b *environmentGitOpsBackend) verifyCandidateCredentials(ctx context.Context, graphID string) error {
	if err := b.verifyCandidatePostgresCredentials(ctx, graphID); err != nil {
		return err
	}
	return b.verifyCandidateObjectStorageCredentials(ctx, graphID)
}

func (b *environmentGitOpsBackend) verifyCandidateObjectStorageCredentials(ctx context.Context, graphID string) error {
	store, ok := b.intent.(state.EnvironmentQualificationObjectStorageStore)
	if !ok || graphID == "" {
		return nil
	}
	checks, err := store.PendingEnvironmentQualificationObjectStorageChecks(ctx, graphID)
	if err != nil {
		return err
	}
	verify := b.server.candidateObjectStorageVerifier
	if verify == nil {
		verify = verifyCandidateObjectStorageCredential
	}
	for _, check := range checks {
		if err := verify(ctx, check); err != nil {
			// Verifier errors are sentinels; they never carry keys or bucket contents.
			slog.WarnContext(ctx, "candidate object storage credential was not verified",
				"request_id", check.RequestID, "credential_id", check.CredentialID, "error", err)
			continue
		}
		if err := store.RecordEnvironmentQualificationObjectStorageReceipt(ctx, check); err != nil && !errors.Is(err, state.ErrBindingPromotionChanged) {
			return err
		}
	}
	return nil
}

func verifyCandidateObjectStorageCredential(ctx context.Context, c state.EnvironmentQualificationObjectStorageCheck) error {
	return verifyCandidateObjectStorageCredentialWith(ctx, hostCredentialIdentities(), hostCredentialHMACKey(), candidateObjectStorageCanary, c)
}

func candidateObjectStorageCanary(ctx context.Context, prefix, mode string, getenv func(string) string) api.ObjectStorageBindingProbeReport {
	if mode == "read" {
		return bindingprobe.ObjectStorage(ctx, prefix, getenv)
	}
	return bindingprobe.ObjectStorageWrite(ctx, prefix, mode, getenv)
}

// verifyCandidateObjectStorageCredentialWith opens each of the six sealed
// deliveries with the host identities, requires its fingerprint, and runs the
// bucket canary in the check's mode with exactly those values.
func verifyCandidateObjectStorageCredentialWith(ctx context.Context, identities []*age.X25519Identity, hmacKey []byte,
	canary objectStorageCanary, c state.EnvironmentQualificationObjectStorageCheck) error {
	if len(identities) == 0 || len(hmacKey) == 0 {
		return errCandidateObjectStorageUnavailable
	}
	keys := object.ObjectStorageBindingSecretKeys(c.Prefix)
	expected := map[string]bool{keys.Endpoint: true, keys.Region: true, keys.Bucket: true,
		keys.AccessKeyID: true, keys.SecretAccessKey: true, keys.AddressingStyle: true}
	values := make(map[string]string, len(expected))
	for _, s := range c.Secrets {
		if !expected[s.Key] {
			return errCandidateObjectStorageChanged
		}
		var matching []*age.X25519Identity
		for _, id := range identities {
			if id != nil && id.Recipient().String() == s.Kid {
				matching = append(matching, id)
			}
		}
		envelope, err := secretbox.OpenMulti(matching, s.Ciphertext)
		if len(matching) == 0 || err != nil || len(envelope) != 1 {
			return errCandidateObjectStorageUnavailable
		}
		value := envelope[s.Key]
		fingerprint, err := secretbox.ValueFingerprint([]byte(value), hmacKey)
		if value == "" || err != nil || subtle.ConstantTimeCompare([]byte(fingerprint), []byte(s.ValueHash)) != 1 {
			return errCandidateObjectStorageChanged
		}
		values[s.Key] = value
	}
	if len(values) != len(expected) {
		return errCandidateObjectStorageChanged
	}
	report := canary(ctx, c.Prefix, c.Mode, func(key string) string { return values[key] })
	wantMode := c.Mode
	if wantMode == "read" {
		wantMode = ""
	}
	if !report.Passed() || report.Mode != wantMode {
		return errCandidateObjectStorageProbe
	}
	return nil
}
