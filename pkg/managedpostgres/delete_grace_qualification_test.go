// adr: 20261029174950 — a qualification run proves the delete grace period's
// hold on a provider that offers one.
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

// holdingQualificationProvider holds databases for deletion but cannot prove it.
type holdingQualificationProvider struct {
	*qualificationProvider
	holds, releases int
}

func (p *holdingQualificationProvider) HoldForDeletion(context.Context, string) error {
	p.holds++
	return nil
}

func (p *holdingQualificationProvider) ReleaseDeletionHold(context.Context, string) error {
	p.releases++
	return nil
}

// provingQualificationProvider also reports what its probe observed.
type provingQualificationProvider struct {
	*holdingQualificationProvider
	evidence DeletionHoldEvidence
}

func (p *provingQualificationProvider) ProbeDeletionHold(context.Context, string, CredentialMaterial) (DeletionHoldEvidence, error) {
	return p.evidence, nil
}

func deletionHoldCheck(report QualificationReport) (QualificationCheck, bool) {
	for _, check := range report.Checks {
		if check.Name == "deletion_hold_probe" {
			return check, true
		}
	}
	return QualificationCheck{}, false
}

func TestQualifyProviderProvesTheDeletionHold(t *testing.T) {
	proven := DeletionHoldEvidence{SuspendedWhileHeld: true, RefusedWhileHeld: true, ResumedAfterRelease: true}
	for _, tc := range []struct {
		name      string
		provider  func() Provider
		wantErr   error
		wantCheck bool
		evidence  *DeletionHoldEvidence
	}{
		{name: "proven hold", provider: func() Provider {
			return &provingQualificationProvider{&holdingQualificationProvider{qualificationProvider: &qualificationProvider{capabilities: testCapabilities()}}, proven}
		}, wantCheck: true, evidence: &proven},
		{name: "a hold that still accepts connections", provider: func() Provider {
			refused := proven
			refused.RefusedWhileHeld = false
			return &provingQualificationProvider{&holdingQualificationProvider{qualificationProvider: &qualificationProvider{capabilities: testCapabilities()}}, refused}
		}, wantErr: ErrQualificationFailed},
		{name: "a hold without a probe", provider: func() Provider {
			return &holdingQualificationProvider{qualificationProvider: &qualificationProvider{capabilities: testCapabilities()}}
		}, wantErr: ErrQualificationFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, err := QualifyProvider(context.Background(), tc.provider(), QualificationOptions{
				ProviderName: "fake", ResourceID: "qualification-hold", Spec: testSpec(), Mutating: true, Timeout: time.Minute,
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("QualifyProvider = %v, want %v", err, tc.wantErr)
			}
			check, found := deletionHoldCheck(report)
			if !found || check.Passed != tc.wantCheck {
				t.Fatalf("deletion_hold_probe = %+v (found %v), want passed=%v", check, found, tc.wantCheck)
			}
			if tc.evidence != nil && (report.DeletionHold == nil || *report.DeletionHold != *tc.evidence || ValidateQualificationReport(report) != nil) {
				t.Fatalf("evidence = %+v, validation = %v", report.DeletionHold, ValidateQualificationReport(report))
			}
		})
	}
}

func TestQualificationReportRequiresAPassingDeletionHoldWhenItHasOne(t *testing.T) {
	report, err := QualifyProvider(context.Background(), &provingQualificationProvider{
		&holdingQualificationProvider{qualificationProvider: &qualificationProvider{capabilities: testCapabilities()}},
		DeletionHoldEvidence{SuspendedWhileHeld: true, RefusedWhileHeld: true, ResumedAfterRelease: true},
	}, QualificationOptions{ProviderName: "fake", ResourceID: "qualification-hold", Spec: testSpec(), Mutating: true, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	failed := report
	failed.DeletionHold = &DeletionHoldEvidence{SuspendedWhileHeld: true, RefusedWhileHeld: true}
	if !errors.Is(ValidateQualificationReport(failed), ErrUnavailable) {
		t.Fatal("a report whose hold did not release validated")
	}
	unchecked := report
	unchecked.Checks = nil
	for _, check := range report.Checks {
		if check.Name != "deletion_hold_probe" {
			unchecked.Checks = append(unchecked.Checks, check)
		}
	}
	if !errors.Is(ValidateQualificationReport(unchecked), ErrInvalid) {
		t.Fatal("hold evidence without its check validated")
	}
}
