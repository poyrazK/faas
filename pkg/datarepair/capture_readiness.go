package datarepair

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// CaptureReadinessOptions comes from trusted application configuration. The
// selected connection supplies database authority; this scope is report context.
type CaptureReadinessOptions struct {
	Scope           Scope  `json:"scope"`
	Table           string `json:"table"`
	Release         string `json:"release"`
	ReceiptProtocol string `json:"receipt_protocol"`
}

type CaptureReadinessReport struct {
	Version         int             `json:"version"`
	Profile         string          `json:"profile"`
	Scope           Scope           `json:"scope"`
	Table           string          `json:"table"`
	Release         string          `json:"release"`
	ReceiptProtocol string          `json:"receipt_protocol"`
	CheckedAt       time.Time       `json:"checked_at"`
	Status          string          `json:"status"`
	Findings        []DoctorFinding `json:"findings"`
}

func (o CaptureReadinessOptions) Normalize() (CaptureReadinessOptions, error) {
	if o.ReceiptProtocol == "" {
		o.ReceiptProtocol = "managed"
	}
	if o.Scope.Validate() != nil || !captureContext(o.Scope.Environment) || !DoctorTable(o.Table) || !captureContext(o.Release) ||
		!slices.Contains([]string{"managed", "customer"}, o.ReceiptProtocol) || o.ReceiptProtocol == "customer" && o.Scope.PlatformTenantID == "" {
		return o, ErrInvalid
	}
	return o, nil
}

func captureContext(s string) bool {
	return s != "" && len(s) <= api.DataRepairCaptureContextMaxBytes && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

func captureMessages() map[string]doctorMessage {
	m := doctorMessages()
	m["writer_connected"] = doctorMessage{"connection", "ready", "The application writer reached a read-only database snapshot.", "Run the capture check again after changing the writer connection."}
	m["writer_unavailable"] = doctorMessage{"connection", "unknown", "The application writer snapshot could not be inspected.", "Check the application's database connection and catalog access, then run the capture check again."}
	m["capture_functions_current"] = doctorMessage{"functions", "ready", "Required capture functions match this probe version.", "Keep SDK capture probes and the owner-installed capture functions in sync."}
	m["capture_functions_incompatible"] = doctorMessage{"functions", "blocked", "Required capture functions are missing, changed or overloaded.", "Have the database owner review the matching gregale data-repair schema and schema --upgrade definitions; restore capture functions without reenrolling or resetting rows."}
	m["writer_permissions_present"] = doctorMessage{"permissions", "ready", "The connected application role has the required capture and receipt privileges.", "Check executor readiness separately; writer readiness does not grant repair authority."}
	m["writer_permissions_missing"] = doctorMessage{"permissions", "blocked", "The connected application role lacks required capture or receipt privileges.", "Have the owner grant only the listed privileges to the application role; plan, approval, apply and maintenance privileges are unnecessary."}
	m["capture_session_valid"] = doctorMessage{"session", "ready", "The writer session uses origin replication mode and supports ordinary write transactions.", "Keep capture enabled on every writer connection; this observation covers one leased session."}
	m["capture_session_invalid"] = doctorMessage{"session", "blocked", "Writer session settings, recovery mode or an active capture context prevent ordinary capture.", "Use an idle writer connection on the primary with origin replication mode, writable defaults and no leftover capture context; assess writes made while triggers were bypassed."}
	m["receipt_ready"] = doctorMessage{"receipt", "ready", "The selected operation receipt inbox has the required catalog shape.", "Qualify any optional milestone, effect or workflow outboxes separately."}
	m["receipt_incompatible"] = doctorMessage{"receipt", "blocked", "The selected operation receipt inbox is missing or incompatible.", "Have the database owner review and install the selected SDK operation receipt schema explicitly; do not reset or remove retained receipts."}
	return m
}

func captureFindingIDs() []string {
	return []string{"connection", "postgres", "installation", "functions", "receipt", "session", "permissions", "enrollment", "fingerprint", "triggers"}
}

func newCaptureReadiness(o CaptureReadinessOptions) CaptureReadinessReport {
	r := CaptureReadinessReport{Version: 1, Profile: "capture", Scope: o.Scope, Table: o.Table, Release: o.Release, ReceiptProtocol: o.ReceiptProtocol, CheckedAt: time.Now().UTC(), Status: "unknown"}
	for _, id := range captureFindingIDs() {
		r.set(id, "not_inspected", nil)
	}
	r.set("connection", "writer_unavailable", nil)
	return r
}

func (r *CaptureReadinessReport) set(id, code string, objects []string) {
	m := captureMessages()[code]
	if objects == nil {
		objects = []string{}
	}
	f := DoctorFinding{ID: id, Code: code, Status: m.status, Summary: m.summary, Action: m.action, Objects: objects}
	for i, old := range r.Findings {
		if old.ID == id {
			r.Findings[i] = f
			return
		}
	}
	r.Findings = append(r.Findings, f)
}

func (r *CaptureReadinessReport) aggregate() {
	r.Status = "ready"
	for _, f := range r.Findings {
		if f.Status == "blocked" {
			r.Status = "blocked"
			return
		}
		if f.Status == "unknown" {
			r.Status = "unknown"
		}
	}
}

func (r CaptureReadinessReport) Valid() bool {
	o, err := (CaptureReadinessOptions{Scope: r.Scope, Table: r.Table, Release: r.Release, ReceiptProtocol: r.ReceiptProtocol}).Normalize()
	if err != nil || o.ReceiptProtocol != r.ReceiptProtocol || r.Version != 1 || r.Profile != "capture" || r.CheckedAt.IsZero() || len(r.Findings) != len(captureFindingIDs()) {
		return false
	}
	seen := map[string]bool{}
	for _, f := range r.Findings {
		m, ok := captureMessages()[f.Code]
		if !ok || !slices.Contains(captureFindingIDs(), f.ID) || seen[f.ID] || m.id != "" && m.id != f.ID || m.status != f.Status || m.summary != f.Summary || m.action != f.Action || f.Objects == nil || len(f.Objects) > api.DataRepairDoctorMaxObjects {
			return false
		}
		seen[f.ID] = true
		for _, object := range f.Objects {
			if object == "" || len(object) > api.DataRepairSourceMaxFilterBytes || !utf8.ValidString(object) || strings.ContainsRune(object, 0) {
				return false
			}
		}
	}
	copy := r
	copy.aggregate()
	raw, err := json.Marshal(r)
	return copy.Status == r.Status && err == nil && len(raw) <= api.DataRepairDoctorMaxResponseBytes
}

type captureFrame struct {
	Findings []struct {
		ID      string   `json:"id"`
		Code    string   `json:"code"`
		Objects []string `json:"objects"`
	} `json:"findings"`
	InspectEnrollment bool `json:"inspect_enrollment"`
}

func (r *CaptureReadinessReport) frame(raw []byte) (bool, error) {
	var f captureFrame
	if len(raw) > api.DataRepairDoctorMaxResponseBytes || json.Unmarshal(raw, &f) != nil || f.Findings == nil {
		return false, ErrUnavailable
	}
	seen := map[string]bool{}
	for _, finding := range f.Findings {
		m, ok := captureMessages()[finding.Code]
		if !ok || m.id != "" && m.id != finding.ID || seen[finding.ID] || !slices.Contains(captureFindingIDs(), finding.ID) || len(finding.Objects) > api.DataRepairDoctorMaxObjects {
			return false, ErrUnavailable
		}
		if finding.ID == "permissions" && finding.Code == "writer_permissions_missing" {
			for _, old := range r.Findings {
				if old.ID == "permissions" && old.Code == "writer_permissions_missing" {
					finding.Objects = append(slices.Clone(old.Objects), finding.Objects...)
				}
			}
		}
		seen[finding.ID] = true
		r.set(finding.ID, finding.Code, finding.Objects)
	}
	return f.InspectEnrollment, nil
}

// CheckCapture observes one application-role session without requiring any
// plan/approval/maintenance access or executing installed capture functions.
func (e *Engine) CheckCapture(ctx context.Context, options CaptureReadinessOptions) (CaptureReadinessReport, error) {
	o, err := options.Normalize()
	if err != nil {
		return CaptureReadinessReport{}, err
	}
	r := newCaptureReadiness(o)
	if err := e.captureSnapshot(ctx, o, &r); err != nil {
		r.set("connection", "writer_unavailable", nil)
	}
	r.aggregate()
	if !r.Valid() {
		return CaptureReadinessReport{}, ErrUnavailable
	}
	return r, nil
}

func (e *Engine) captureSnapshot(ctx context.Context, o CaptureReadinessOptions, r *CaptureReadinessReport) (snapshotErr error) {
	if e == nil || e.Pool == nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.DataRepairDoctorTimeoutSeconds)*time.Second)
	defer cancel()
	tx, err := e.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() {
		clean, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(api.DataRepairDoctorTimeoutSeconds)*time.Second)
		defer stop()
		if err := tx.Rollback(clean); err != nil && !errors.Is(err, pgx.ErrTxClosed) && snapshotErr == nil {
			snapshotErr = err
		}
	}()
	p := captureProgram()
	if _, err := tx.Exec(ctx, p.SetupSQL); err != nil {
		return err
	}
	var raw []byte
	if err := tx.QueryRow(ctx, p.CatalogSQL, o.ReceiptProtocol).Scan(&raw); err != nil {
		return err
	}
	inspect, err := r.frame(raw)
	if err != nil || !inspect {
		return err
	}
	if err := tx.QueryRow(ctx, p.EnrollmentSQL, o.Table).Scan(&raw); err != nil {
		return err
	}
	_, err = r.frame(raw)
	return err
}
