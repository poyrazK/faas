package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

type unsettledQualificationProvider struct {
	qualificationProvider
	invalidUsage bool
}

func (p *unsettledQualificationProvider) Usage(context.Context, string, UsageWindow) (Usage, error) {
	if p.invalidUsage {
		return Usage{}, nil
	}
	return Usage{}, ErrNotFound
}

func TestQualificationUsageDiagnosticsContinueButCannotApprove(t *testing.T) {
	for _, invalidUsage := range []bool{false, true} {
		for _, continueAfterFailure := range []bool{false, true} {
			p := &unsettledQualificationProvider{qualificationProvider: qualificationProvider{capabilities: testCapabilities()}, invalidUsage: invalidUsage}
			report, err := QualifyProvider(t.Context(), p, QualificationOptions{ProviderName: "fake", ResourceID: "usage-diagnostic", Spec: testSpec(), Mutating: true, ContinueAfterUsageFailure: continueAfterFailure})
			if !errors.Is(err, ErrQualificationFailed) || !p.deleted || (continueAfterFailure && (p.issue == 0 || p.restore == 0 || report.Restore == nil || !report.Restore.Deleted)) ||
				(!continueAfterFailure && (p.issue != 0 || p.restore != 0)) {
				t.Fatalf("continued=%v invalid=%v: report=%+v provider=%+v err=%v", continueAfterFailure, invalidUsage, report, p, err)
			}
			lifecycle := passingLifecycleQualificationReport()
			if _, err := BuildQualificationApproval(report, &lifecycle, "backend", "fingerprint", nil, time.Now().UTC(), time.Hour); err == nil {
				t.Fatal("failed usage diagnostic authorized provisioning")
			}
			failed := false
			for _, check := range report.Checks {
				if (check.Name == "usage" || check.Name == "usage_valid") && !check.Passed {
					failed = true
				}
			}
			if !failed {
				t.Fatal("mandatory usage failure was erased")
			}
		}
	}
}

type failingQualificationCleanupProvider struct{ qualificationProvider }

func (*failingQualificationCleanupProvider) RevokeCredentials(context.Context, CredentialRequest) error {
	return errors.New("secret cleanup detail")
}

func (*failingQualificationCleanupProvider) CleanupRestore(context.Context, string, CredentialMaterial) error {
	return ErrUnavailable
}

func (*failingQualificationCleanupProvider) Delete(context.Context, DeleteRequest) (DeleteResult, error) {
	return DeleteResult{}, ErrConflict
}

func TestQualificationReportsCleanupFailuresAlongsidePrimaryFailure(t *testing.T) {
	p := &failingQualificationCleanupProvider{qualificationProvider: qualificationProvider{capabilities: testCapabilities(), isolationErr: ErrUnavailable}}
	report, err := QualifyProvider(t.Context(), p, QualificationOptions{ProviderName: "fake", ResourceID: "cleanup-diagnostic", Spec: testSpec(), Mutating: true})
	if !errors.Is(err, ErrQualificationFailed) {
		t.Fatal(err)
	}
	for _, name := range []string{"restore_credentials_isolated", "cleanup_restore_probe", "cleanup_credentials", "cleanup_restore_credentials", "cleanup_restore", "cleanup_resource"} {
		found := false
		for _, check := range report.Checks {
			if check.Name == name && !check.Passed && check.Error != "secret cleanup detail" {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing cleanup failure %s: %+v", name, report.Checks)
		}
	}
}
