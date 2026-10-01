package managedpostgres

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeSupplierGateArtifacts(t *testing.T, registry *Registry, now time.Time) (string, string) {
	t.Helper()
	backend, err := registry.Default(registry.DefaultRegion)
	if err != nil {
		t.Fatal(err)
	}
	approval := SupplierApproval{
		Version:             SupplierApprovalVersion,
		SupplierName:        backend.SupplierName,
		SubprocessorID:      "managed-postgres-test-provider",
		BackendID:           backend.ID,
		BackendFingerprint:  backend.Fingerprint,
		Decision:            "accepted",
		ReviewerReference:   "reviewer-platform-security-01",
		AssessmentReference: "risk-2026-09",
		AgreementReference:  "dpa-2026-09",
		ReviewedAt:          now.AddDate(0, -1, 0),
		ExpiresAt:           now.AddDate(0, 6, 0),
	}
	register := SubprocessorRegister{
		NoticeWindowDays: SupplierNoticeWindowDays,
		SubProcessors: []SubprocessorRecord{{
			ID:                approval.SubprocessorID,
			Category:          "database",
			Vendor:            backend.SupplierName,
			DPASigned:         true,
			DPAReference:      "DPA §7",
			NoticePublishedAt: stringPointer("2026-01-01"),
			EffectiveDate:     stringPointer("2026-01-31"),
		}},
	}
	dir := t.TempDir()
	approvalPath := filepath.Join(dir, "supplier-approval.json")
	registerPath := filepath.Join(dir, "subprocessors.json")
	writeSupplierJSON(t, approvalPath, approval)
	writeSupplierJSON(t, registerPath, register)
	return approvalPath, registerPath
}

func stringPointer(value string) *string { return &value }

func writeSupplierJSON(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestVerifySupplierApprovalRequiresCurrentAcceptedSupplierEvidence(t *testing.T) {
	provider := &qualificationProvider{capabilities: testCapabilities()}
	registry := testRegistry(t, provider, nil)
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	approvalPath, registerPath := writeSupplierGateArtifacts(t, registry, now)
	approval, err := LoadSupplierApproval(approvalPath)
	if err != nil {
		t.Fatal(err)
	}
	register, err := LoadSubprocessorRegister(registerPath)
	if err != nil {
		t.Fatal(err)
	}
	if readiness := registry.VerifySupplierApproval(approval, register, now); !readiness.Ready {
		t.Fatalf("valid supplier evidence is not ready: %+v", readiness)
	}
	if readiness := registry.VerifySupplierApprovalFiles(approvalPath, registerPath, now); !readiness.Ready {
		t.Fatalf("valid supplier evidence files are not ready: %+v", readiness)
	}
	conditionalApproval := approval
	conditionalApproval.Decision = "accepted_with_conditions"
	conditionalApproval.Conditions = "Confirm deletion SLA"
	conditionalApproval.ConditionsSatisfied = true
	conditionalApproval.ConditionsEvidence = "deletion-sla-evidence-2026-09"
	if readiness := registry.VerifySupplierApproval(conditionalApproval, register, now); !readiness.Ready {
		t.Fatalf("verified conditional supplier acceptance is not ready: %+v", readiness)
	}
	legacyRegistry := testRegistry(t, provider, func(config *Config) {
		config.Backends[0].SupplierName = ""
	})
	if readiness := legacyRegistry.VerifySupplierApproval(approval, register, now); readiness.Ready || !containsReason(readiness.Reasons, "supplier_name_mismatch") {
		t.Fatalf("supplier approval passed without configured supplier identity: %+v", readiness)
	}

	tests := map[string]struct {
		mutateApproval func(*SupplierApproval)
		mutateRegister func(*SubprocessorRegister)
		wantReason     string
	}{
		"wrong backend":      {mutateApproval: func(candidate *SupplierApproval) { candidate.BackendID = "other-backend" }, wantReason: "supplier_backend_mismatch"},
		"wrong fingerprint":  {mutateApproval: func(candidate *SupplierApproval) { candidate.BackendFingerprint = "other-fingerprint" }, wantReason: "supplier_backend_fingerprint_mismatch"},
		"wrong supplier":     {mutateApproval: func(candidate *SupplierApproval) { candidate.SupplierName = "Other Provider" }, wantReason: "supplier_name_mismatch"},
		"decision pending":   {mutateApproval: func(candidate *SupplierApproval) { candidate.Decision = "pending" }, wantReason: "supplier_decision_not_accepted"},
		"conditions missing": {mutateApproval: func(candidate *SupplierApproval) { candidate.Decision = "accepted_with_conditions" }, wantReason: "supplier_conditions_missing"},
		"conditions unverified": {mutateApproval: func(candidate *SupplierApproval) {
			candidate.Decision = "accepted_with_conditions"
			candidate.Conditions = "Confirm deletion SLA"
		}, wantReason: "supplier_conditions_unverified"},
		"assessment missing":            {mutateApproval: func(candidate *SupplierApproval) { candidate.AssessmentReference = "" }, wantReason: "supplier_assessment_evidence_missing"},
		"reviewer missing":              {mutateApproval: func(candidate *SupplierApproval) { candidate.ReviewerReference = "" }, wantReason: "supplier_reviewer_missing"},
		"agreement missing":             {mutateApproval: func(candidate *SupplierApproval) { candidate.AgreementReference = "" }, wantReason: "supplier_agreement_evidence_missing"},
		"expired review":                {mutateApproval: func(candidate *SupplierApproval) { candidate.ExpiresAt = now }, wantReason: "supplier_review_expired_or_invalid"},
		"review lasts too long":         {mutateApproval: func(candidate *SupplierApproval) { candidate.ExpiresAt = candidate.ReviewedAt.AddDate(0, 13, 0) }, wantReason: "supplier_review_expired_or_invalid"},
		"supplier absent from register": {mutateApproval: func(candidate *SupplierApproval) { candidate.SubprocessorID = "unknown-provider" }, wantReason: "supplier_not_listed"},
		"DPA unsigned":                  {mutateRegister: func(candidate *SubprocessorRegister) { candidate.SubProcessors[0].DPASigned = false }, wantReason: "supplier_dpa_unverified"},
		"DPA reference missing":         {mutateRegister: func(candidate *SubprocessorRegister) { candidate.SubProcessors[0].DPAReference = " " }, wantReason: "supplier_dpa_unverified"},
		"wrong category":                {mutateRegister: func(candidate *SubprocessorRegister) { candidate.SubProcessors[0].Category = "hosting" }, wantReason: "supplier_category_mismatch"},
		"register vendor mismatch":      {mutateRegister: func(candidate *SubprocessorRegister) { candidate.SubProcessors[0].Vendor = "Other Provider" }, wantReason: "supplier_register_name_mismatch"},
		"notice too short": {mutateRegister: func(candidate *SubprocessorRegister) {
			candidate.SubProcessors[0].EffectiveDate = stringPointer("2026-01-30")
		}, wantReason: "supplier_notice_window_not_satisfied"},
		"notice not effective": {mutateRegister: func(candidate *SubprocessorRegister) {
			candidate.SubProcessors[0].EffectiveDate = stringPointer("2026-09-08")
		}, wantReason: "supplier_notice_not_effective"},
		"notice missing":      {mutateRegister: func(candidate *SubprocessorRegister) { candidate.SubProcessors[0].NoticePublishedAt = nil }, wantReason: "supplier_notice_dates_missing_or_invalid"},
		"wrong notice period": {mutateRegister: func(candidate *SubprocessorRegister) { candidate.NoticeWindowDays = 14 }, wantReason: "supplier_register_notice_window_invalid"},
		"removal record incomplete": {mutateRegister: func(candidate *SubprocessorRegister) {
			candidate.SubProcessors[0].EffectiveUntil = stringPointer("2026-09-30")
		}, wantReason: "supplier_register_status_invalid"},
		"supplier no longer active": {mutateRegister: func(candidate *SubprocessorRegister) {
			candidate.SubProcessors[0].EffectiveUntil = stringPointer("2026-09-06")
			candidate.SubProcessors[0].RemovalReason = "service ended"
		}, wantReason: "supplier_not_active"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			candidateApproval := approval
			candidateRegister := register
			candidateRegister.SubProcessors = append([]SubprocessorRecord(nil), register.SubProcessors...)
			if test.mutateApproval != nil {
				test.mutateApproval(&candidateApproval)
			}
			if test.mutateRegister != nil {
				test.mutateRegister(&candidateRegister)
			}
			readiness := registry.VerifySupplierApproval(candidateApproval, candidateRegister, now)
			if readiness.Ready || !containsReason(readiness.Reasons, test.wantReason) {
				t.Fatalf("readiness = %+v, want blocking reason %q", readiness, test.wantReason)
			}
		})
	}
}

func TestSupplierApprovalFilesFailClosedAndUseStrictApprovalJSON(t *testing.T) {
	provider := &qualificationProvider{capabilities: testCapabilities()}
	registry := testRegistry(t, provider, nil)
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	if readiness := registry.VerifySupplierApprovalFiles("", "", now); readiness.Ready || !containsReason(readiness.Reasons, "supplier_approval_unavailable") {
		t.Fatalf("missing files readiness = %+v", readiness)
	}
	approvalPath, registerPath := writeSupplierGateArtifacts(t, registry, now)
	if err := os.WriteFile(approvalPath, []byte(`{"version":1,"unexpected":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSupplierApproval(approvalPath); err == nil {
		t.Fatal("unknown approval field was accepted")
	}

	// The canonical register has unrelated public fields; those are allowed so
	// this small verifier stays forward compatible with the published schema.
	if err := os.WriteFile(registerPath, []byte(`{"notice_window_days":30,"sub_processors":[{"id":"postgres-hosting","category":"database","vendor":"Neon","dpa_signed":true,"dpa_reference":"DPA","notice_published_at":"2026-01-01","effective_date":"2026-01-31","service":"managed Postgres"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSubprocessorRegister(registerPath); err != nil {
		t.Fatalf("register unknown field should be ignored: %v", err)
	}
}

func containsReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}
