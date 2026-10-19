// adr: 20261019121546 — apid verifies enforced candidates' sealed bucket deliveries.
package apidserver_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type candidateStorageIntent struct {
	state.EnvironmentGitOpsIntentStore
	checks   []state.EnvironmentQualificationObjectStorageCheck
	recorded []string
}

func (s *candidateStorageIntent) PendingEnvironmentQualificationObjectStorageChecks(context.Context, string) ([]state.EnvironmentQualificationObjectStorageCheck, error) {
	return s.checks, nil
}

func (s *candidateStorageIntent) RecordEnvironmentQualificationObjectStorageReceipt(_ context.Context, c state.EnvironmentQualificationObjectStorageCheck) error {
	if c.CredentialID == "changed" {
		return state.ErrBindingPromotionChanged
	}
	s.recorded = append(s.recorded, c.CredentialID)
	return nil
}

func TestCandidateObjectStorageCredentialStepRecordsOnlyVerifiedCredentials(t *testing.T) {
	intent := &candidateStorageIntent{checks: []state.EnvironmentQualificationObjectStorageCheck{
		{CredentialID: "verified"}, {CredentialID: "rejected"}, {CredentialID: "changed"},
	}}
	err := apidserver.VerifyCandidateObjectStorageCredentialsForTest(t.Context(), intent, uuid.NewString(),
		func(_ context.Context, c state.EnvironmentQualificationObjectStorageCheck) error {
			if c.CredentialID == "rejected" {
				return errors.New("canary failed")
			}
			return nil
		})
	if err != nil || len(intent.recorded) != 1 || intent.recorded[0] != "verified" {
		t.Fatalf("recorded %v, err %v", intent.recorded, err)
	}
}

func sealedStorageCheck(t *testing.T, identity *age.X25519Identity, hmac []byte, mode string) (state.EnvironmentQualificationObjectStorageCheck, map[string]string) {
	t.Helper()
	values := map[string]string{"_ENDPOINT": "https://s3.example.test", "_REGION": "eu", "_BUCKET": "assets",
		"_ACCESS_KEY_ID": "AKIDEXAMPLE", "_SECRET_ACCESS_KEY": "very-secret", "_ADDRESSING_STYLE": "path"}
	check := state.EnvironmentQualificationObjectStorageCheck{RequestID: uuid.NewString(), Attempt: 1, Scope: "production",
		CredentialID: uuid.NewString(), Prefix: "GREGALE_S3_ASSETS", Permission: "read_write", Mode: mode}
	plain := map[string]string{}
	for suffix, value := range values {
		key := check.Prefix + suffix
		blob, err := secretbox.Seal(identity.Recipient(), secretbox.Envelope{key: value})
		if err != nil {
			t.Fatal(err)
		}
		hash, err := secretbox.ValueFingerprint([]byte(value), hmac)
		if err != nil {
			t.Fatal(err)
		}
		check.Secrets = append(check.Secrets, state.EnvironmentQualificationObjectStorageSecret{Key: key, DeliveryVersion: 1,
			Ciphertext: blob, Kid: identity.Recipient().String(), ValueHash: hash})
		plain[key] = value
	}
	return check, plain
}

func TestCandidateObjectStorageVerifierChecksSealedDeliveries(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	hmac := []byte(strings.Repeat("h", 32))
	ids := []*age.X25519Identity{identity}
	for _, mode := range []string{"read", "write", "read_write"} {
		check, plain := sealedStorageCheck(t, identity, hmac, mode)
		calls := 0
		canary := func(_ context.Context, prefix, gotMode string, getenv func(string) string) api.ObjectStorageBindingProbeReport {
			calls++
			for key, value := range plain {
				if getenv(key) != value {
					t.Fatalf("canary received a different %s", key)
				}
			}
			if prefix != check.Prefix || gotMode != mode {
				t.Fatalf("canary input %s %s", prefix, gotMode)
			}
			return passedStorageReport(mode)
		}
		if err := apidserver.VerifyCandidateObjectStorageCredentialForTest(t.Context(), check, ids, hmac, canary); err != nil || calls != 1 {
			t.Fatalf("%s delivery was not verified: %v", mode, err)
		}
	}

	check, _ := sealedStorageCheck(t, identity, hmac, "read")
	passing := func(context.Context, string, string, func(string) string) api.ObjectStorageBindingProbeReport {
		return passedStorageReport("read")
	}
	for name, mutate := range map[string]func(*state.EnvironmentQualificationObjectStorageCheck){
		"value hash":   func(c *state.EnvironmentQualificationObjectStorageCheck) { c.Secrets[0].ValueHash = "wrong" },
		"ciphertext":   func(c *state.EnvironmentQualificationObjectStorageCheck) { c.Secrets[1].Ciphertext = []byte("wrong") },
		"foreign key":  func(c *state.EnvironmentQualificationObjectStorageCheck) { c.Secrets[2].Key = "OTHER_BUCKET" },
		"missing key":  func(c *state.EnvironmentQualificationObjectStorageCheck) { c.Secrets = c.Secrets[:5] },
		"other prefix": func(c *state.EnvironmentQualificationObjectStorageCheck) { c.Prefix = "GREGALE_S3_OTHER" },
	} {
		changed := check
		changed.Secrets = append([]state.EnvironmentQualificationObjectStorageSecret(nil), check.Secrets...)
		mutate(&changed)
		if err := apidserver.VerifyCandidateObjectStorageCredentialForTest(t.Context(), changed, ids, hmac, passing); err == nil {
			t.Fatalf("%s substitution was verified", name)
		}
	}
	failed := func(context.Context, string, string, func(string) string) api.ObjectStorageBindingProbeReport {
		return api.ObjectStorageBindingProbeReport{Error: "object-storage authorization failed"}
	}
	if err := apidserver.VerifyCandidateObjectStorageCredentialForTest(t.Context(), check, ids, hmac, failed); err == nil || strings.Contains(err.Error(), "very-secret") {
		t.Fatal("canary failure was hidden or leaked the credential", err)
	}
	// A read canary cannot stand in for the write evidence a requirement needs.
	write, _ := sealedStorageCheck(t, identity, hmac, "write")
	if err := apidserver.VerifyCandidateObjectStorageCredentialForTest(t.Context(), write, ids, hmac, passing); err == nil {
		t.Fatal("read canary satisfied a write check")
	}
}

func passedStorageReport(mode string) api.ObjectStorageBindingProbeReport {
	ok := api.ObjectStorageBindingProbeCheck{Status: "passed"}
	report := api.ObjectStorageBindingProbeReport{Environment: ok, Configuration: ok, Connection: ok, Authorization: ok, BucketAccess: ok}
	if mode != "read" {
		report.Mode = mode
		download := api.ObjectStorageBindingProbeCheck{Status: "not_checked"}
		if mode == "read_write" {
			download = ok
		}
		report.Upload, report.Cleanup, report.Download = &ok, &ok, &download
	}
	return report
}
