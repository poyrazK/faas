package managedpostgres

import (
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	SupplierApprovalVersion  = 1
	SupplierNoticeWindowDays = 30
	maxSupplierArtifactBytes = 1 << 20
)

var opaqueEvidenceReference = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// SupplierApproval is an operator-owned record that links a provider
// qualification to a reviewed supplier decision and its restricted evidence.
// It contains references only; contracts and reports remain in the evidence
// store.
type SupplierApproval struct {
	Version             int       `json:"version"`
	SupplierName        string    `json:"supplier_name"`
	SubprocessorID      string    `json:"subprocessor_id"`
	BackendID           string    `json:"backend_id"`
	BackendFingerprint  string    `json:"backend_fingerprint"`
	Decision            string    `json:"decision"`
	ReviewerReference   string    `json:"reviewer_reference"`
	AssessmentReference string    `json:"assessment_reference"`
	AgreementReference  string    `json:"agreement_reference"`
	ReviewedAt          time.Time `json:"reviewed_at"`
	ExpiresAt           time.Time `json:"expires_at"`
	Conditions          string    `json:"conditions,omitempty"`
	ConditionsSatisfied bool      `json:"conditions_satisfied,omitempty"`
	ConditionsEvidence  string    `json:"conditions_evidence_reference,omitempty"`
}

// SubprocessorRegister contains only the public fields needed to verify the
// active processor, DPA claim, and notice dates for a managed-PostgreSQL
// provider. Unknown public-register fields are ignored for forward
// compatibility.
type SubprocessorRegister struct {
	NoticeWindowDays int                  `json:"notice_window_days"`
	SubProcessors    []SubprocessorRecord `json:"sub_processors"`
}

type SubprocessorRecord struct {
	ID                string  `json:"id"`
	Category          string  `json:"category"`
	Vendor            string  `json:"vendor"`
	DPASigned         bool    `json:"dpa_signed"`
	DPAReference      string  `json:"dpa_reference"`
	NoticePublishedAt *string `json:"notice_published_at"`
	EffectiveDate     *string `json:"effective_date"`
	EffectiveUntil    *string `json:"effective_until"`
	RemovalReason     string  `json:"removal_reason"`
}

// LoadSupplierApproval reads a small, operator-owned supplier decision file.
// Unknown fields and trailing JSON are rejected so typos cannot silently
// weaken a rollout approval.
func LoadSupplierApproval(path string) (SupplierApproval, error) {
	var approval SupplierApproval
	if err := readJSON(path, &approval, true); err != nil {
		return SupplierApproval{}, err
	}
	return approval, nil
}

// LoadSubprocessorRegister loads the public register snapshot installed with
// the daemon. The runtime compares its active processor entry with the
// configured backend and supplier approval before allowing provisioning.
func LoadSubprocessorRegister(path string) (SubprocessorRegister, error) {
	var register SubprocessorRegister
	if err := readJSON(path, &register, false); err != nil {
		return SubprocessorRegister{}, err
	}
	if register.NoticeWindowDays != SupplierNoticeWindowDays || len(register.SubProcessors) == 0 || len(register.SubProcessors) > 500 {
		return SubprocessorRegister{}, ErrInvalid
	}
	seen := make(map[string]struct{}, len(register.SubProcessors))
	for _, item := range register.SubProcessors {
		if strings.TrimSpace(item.ID) == "" || item.ID != strings.TrimSpace(item.ID) || strings.TrimSpace(item.Vendor) == "" || strings.TrimSpace(item.Category) == "" {
			return SubprocessorRegister{}, ErrInvalid
		}
		if _, exists := seen[item.ID]; exists {
			return SubprocessorRegister{}, ErrInvalid
		}
		seen[item.ID] = struct{}{}
	}
	return register, nil
}

func readJSON(path string, target any, rejectUnknownFields bool) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return ErrInvalid
	}
	file, err := os.Open(path) //nolint:forbidigo // operator-owned deployment evidence
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || info.Size() > maxSupplierArtifactBytes {
		return ErrInvalid
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxSupplierArtifactBytes))
	if rejectUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return ErrInvalid
	}
	return nil
}

// VerifySupplierApproval requires an active register entry and an unexpired
// internal decision for the exact backend placement. Public notice dates are
// parsed using the same YYYY-MM-DD convention as docs/compliance/subprocessors.json.
func (r *Registry) VerifySupplierApproval(approval SupplierApproval, register SubprocessorRegister, now time.Time) QualificationReadiness {
	var reasons []string
	add := func(reason string) { reasons = append(reasons, reason) }
	if r == nil {
		return QualificationReadiness{Reasons: []string{"registry_unavailable"}}
	}
	regions := r.Regions()
	if len(regions) != 1 {
		add("default_backend_count_invalid")
	} else {
		backend, err := r.Default(regions[0])
		if err != nil {
			add("default_backend_unavailable")
		} else {
			if !strings.EqualFold(strings.TrimSpace(approval.SupplierName), backend.SupplierName) {
				add("supplier_name_mismatch")
			}
			if approval.BackendID != backend.ID {
				add("supplier_backend_mismatch")
			}
			if approval.BackendFingerprint != backend.Fingerprint {
				add("supplier_backend_fingerprint_mismatch")
			}
		}
	}
	if approval.Version != SupplierApprovalVersion {
		add("supplier_approval_version_invalid")
	}
	if strings.TrimSpace(approval.SubprocessorID) == "" {
		add("supplier_subprocessor_id_missing")
	}
	if !opaqueEvidenceReference.MatchString(approval.AssessmentReference) {
		add("supplier_assessment_evidence_missing")
	}
	if !opaqueEvidenceReference.MatchString(approval.ReviewerReference) {
		add("supplier_reviewer_missing")
	}
	if !opaqueEvidenceReference.MatchString(approval.AgreementReference) {
		add("supplier_agreement_evidence_missing")
	}
	switch approval.Decision {
	case "accepted":
	case "accepted_with_conditions":
		if strings.TrimSpace(approval.Conditions) == "" || len(approval.Conditions) > 1000 {
			add("supplier_conditions_missing")
		}
		if !approval.ConditionsSatisfied || !opaqueEvidenceReference.MatchString(approval.ConditionsEvidence) {
			add("supplier_conditions_unverified")
		}
	default:
		add("supplier_decision_not_accepted")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if approval.ReviewedAt.IsZero() || approval.ExpiresAt.IsZero() || approval.ReviewedAt.After(now) || !approval.ExpiresAt.After(now) || !approval.ExpiresAt.After(approval.ReviewedAt) || approval.ExpiresAt.After(approval.ReviewedAt.AddDate(0, 12, 0)) {
		add("supplier_review_expired_or_invalid")
	}
	if register.NoticeWindowDays != SupplierNoticeWindowDays {
		add("supplier_register_notice_window_invalid")
	}
	var selected *SubprocessorRecord
	for index := range register.SubProcessors {
		if register.SubProcessors[index].ID == approval.SubprocessorID {
			selected = &register.SubProcessors[index]
			break
		}
	}
	if selected == nil {
		add("supplier_not_listed")
	} else {
		if (selected.EffectiveUntil == nil) != (strings.TrimSpace(selected.RemovalReason) == "") {
			add("supplier_register_status_invalid")
		}
		if selected.EffectiveUntil != nil {
			untilAt, untilOK := supplierRegisterDate(selected.EffectiveUntil)
			if !untilOK {
				add("supplier_register_status_invalid")
			} else if now.Format("2006-01-02") > untilAt.Format("2006-01-02") {
				add("supplier_not_active")
			}
		}
		if !strings.EqualFold(strings.TrimSpace(selected.Category), "database") {
			add("supplier_category_mismatch")
		}
		if !strings.EqualFold(strings.TrimSpace(selected.Vendor), strings.TrimSpace(approval.SupplierName)) {
			add("supplier_register_name_mismatch")
		}
		if !selected.DPASigned || strings.TrimSpace(selected.DPAReference) == "" {
			add("supplier_dpa_unverified")
		}
		noticeAt, noticeOK := supplierRegisterDate(selected.NoticePublishedAt)
		effectiveAt, effectiveOK := supplierRegisterDate(selected.EffectiveDate)
		if !noticeOK || !effectiveOK {
			add("supplier_notice_dates_missing_or_invalid")
		} else {
			if effectiveAt.Sub(noticeAt) < time.Duration(SupplierNoticeWindowDays)*24*time.Hour {
				add("supplier_notice_window_not_satisfied")
			}
			if now.Format("2006-01-02") < effectiveAt.Format("2006-01-02") {
				add("supplier_notice_not_effective")
			}
		}
	}
	return QualificationReadiness{Ready: len(reasons) == 0, Reasons: reasons}
}

func supplierRegisterDate(value *string) (time.Time, bool) {
	if value == nil {
		return time.Time{}, false
	}
	parsed, err := time.Parse("2006-01-02", strings.TrimSpace(*value))
	return parsed.UTC(), err == nil
}

// VerifySupplierApprovalFiles loads both the protected decision record and
// current public-register snapshot, then checks them against the configured
// backend. It returns stable reason codes and never includes file contents.
func (r *Registry) VerifySupplierApprovalFiles(approvalPath, registerPath string, now time.Time) QualificationReadiness {
	approval, err := LoadSupplierApproval(approvalPath)
	if err != nil {
		return QualificationReadiness{Reasons: []string{"supplier_approval_unavailable"}}
	}
	register, err := LoadSubprocessorRegister(registerPath)
	if err != nil {
		return QualificationReadiness{Reasons: []string{"supplier_register_unavailable"}}
	}
	return r.VerifySupplierApproval(approval, register, now)
}
