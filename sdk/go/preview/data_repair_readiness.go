package faas

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

//go:embed data_repair_readiness_probe.json
var dataRepairReadinessProbe []byte

var ErrInvalidDataRepairReadiness = errors.New("invalid data repair capture readiness options")

type DataRepairCaptureScope struct {
	AccountID        string `json:"account_id"`
	AppID            string `json:"app_id"`
	Environment      string `json:"environment"`
	PlatformTenantID string `json:"platform_tenant_id,omitempty"`
}

// Scope is trusted report context, not authorization inferred from a database.
type DataRepairCaptureReadinessOptions struct {
	Scope           DataRepairCaptureScope `json:"scope"`
	Table           string                 `json:"table"`
	Release         string                 `json:"release"`
	ReceiptProtocol string                 `json:"receipt_protocol"`
}

type DataRepairReadinessFinding struct {
	ID      string   `json:"id"`
	Status  string   `json:"status"`
	Code    string   `json:"code"`
	Summary string   `json:"summary"`
	Action  string   `json:"action"`
	Objects []string `json:"objects"`
}

// This observation covers one leased writer session, never executor readiness,
// capture coverage, business invariants, rehearsal evidence or repair authority.
type DataRepairCaptureReadinessReport struct {
	Version         int                          `json:"version"`
	Profile         string                       `json:"profile"`
	Scope           DataRepairCaptureScope       `json:"scope"`
	Table           string                       `json:"table"`
	Release         string                       `json:"release"`
	ReceiptProtocol string                       `json:"receipt_protocol"`
	CheckedAt       time.Time                    `json:"checked_at"`
	Status          string                       `json:"status"`
	Findings        []DataRepairReadinessFinding `json:"findings"`
}

type repairReadinessMessage struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
	Action  string `json:"action"`
}
type repairReadinessProgram struct {
	Version          int                               `json:"version"`
	TimeoutMS        int                               `json:"timeout_ms"`
	MaxResponseBytes int                               `json:"max_response_bytes"`
	MaxObjects       int                               `json:"max_objects"`
	MaxTableBytes    int                               `json:"max_table_bytes"`
	MaxContextBytes  int                               `json:"max_context_bytes"`
	FindingIDs       []string                          `json:"finding_ids"`
	Messages         map[string]repairReadinessMessage `json:"messages"`
	SetupSQL         string                            `json:"setup_sql"`
	CatalogSQL       string                            `json:"catalog_sql"`
	EnrollmentSQL    string                            `json:"enrollment_sql"`
}

func repairProbe() repairReadinessProgram {
	var p repairReadinessProgram
	_ = json.Unmarshal(dataRepairReadinessProbe, &p)
	return p
}

func normalizeCaptureReadiness(o DataRepairCaptureReadinessOptions, p repairReadinessProgram) (DataRepairCaptureReadinessOptions, error) {
	if o.ReceiptProtocol == "" {
		o.ReceiptProtocol = "managed"
	}
	for _, id := range []string{o.Scope.AccountID, o.Scope.AppID} {
		if !operationUUID(id) || id != strings.ToLower(id) {
			return o, ErrInvalidDataRepairReadiness
		}
	}
	if id := o.Scope.PlatformTenantID; id != "" && (!operationUUID(id) || id != strings.ToLower(id)) {
		return o, ErrInvalidDataRepairReadiness
	}
	for _, s := range []string{o.Scope.Environment, o.Release} {
		if s == "" || len(s) > p.MaxContextBytes || !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			return o, ErrInvalidDataRepairReadiness
		}
	}
	if o.Table == "" || len(o.Table) > p.MaxTableBytes || strings.TrimSpace(o.Table) != o.Table || !strings.Contains(o.Table, ".") || !utf8.ValidString(o.Table) || strings.ContainsRune(o.Table, 0) ||
		(o.ReceiptProtocol != "managed" && o.ReceiptProtocol != "customer") || o.ReceiptProtocol == "customer" && o.Scope.PlatformTenantID == "" {
		return o, ErrInvalidDataRepairReadiness
	}
	return o, nil
}

func (r *DataRepairCaptureReadinessReport) set(p repairReadinessProgram, id, code string, objects []string) {
	m := p.Messages[code]
	if objects == nil {
		objects = []string{}
	}
	f := DataRepairReadinessFinding{ID: id, Status: m.Status, Code: code, Summary: m.Summary, Action: m.Action, Objects: objects}
	for i, old := range r.Findings {
		if old.ID == id {
			r.Findings[i] = f
			return
		}
	}
	r.Findings = append(r.Findings, f)
}

func (r *DataRepairCaptureReadinessReport) aggregate() {
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

func (r *DataRepairCaptureReadinessReport) frame(p repairReadinessProgram, raw []byte) (bool, error) {
	var frame struct {
		Findings []struct {
			ID      string   `json:"id"`
			Code    string   `json:"code"`
			Objects []string `json:"objects"`
		} `json:"findings"`
		InspectEnrollment bool `json:"inspect_enrollment"`
	}
	if len(raw) > p.MaxResponseBytes || json.Unmarshal(raw, &frame) != nil || frame.Findings == nil {
		return false, ErrInvalidDataRepairReadiness
	}
	seen := map[string]bool{}
	for _, f := range frame.Findings {
		m, ok := p.Messages[f.Code]
		if !ok || m.ID != "" && m.ID != f.ID || seen[f.ID] || !repairReadinessID(p, f.ID) || len(f.Objects) > p.MaxObjects {
			return false, ErrInvalidDataRepairReadiness
		}
		seen[f.ID] = true
		if f.ID == "permissions" && f.Code == "writer_permissions_missing" {
			for _, old := range r.Findings {
				if old.ID == f.ID && old.Code == f.Code {
					f.Objects = append(append([]string{}, old.Objects...), f.Objects...)
				}
			}
		}
		r.set(p, f.ID, f.Code, f.Objects)
	}
	return frame.InspectEnrollment, nil
}

func repairReadinessID(p repairReadinessProgram, id string) bool {
	for _, known := range p.FindingIDs {
		if known == id {
			return true
		}
	}
	return false
}

func (r DataRepairCaptureReadinessReport) Valid() bool {
	p := repairProbe()
	o, err := normalizeCaptureReadiness(DataRepairCaptureReadinessOptions{Scope: r.Scope, Table: r.Table, Release: r.Release, ReceiptProtocol: r.ReceiptProtocol}, p)
	if err != nil || r.Version != p.Version || r.Profile != "capture" || r.CheckedAt.IsZero() || o.ReceiptProtocol != r.ReceiptProtocol || len(r.Findings) != len(p.FindingIDs) {
		return false
	}
	seen := map[string]bool{}
	for _, f := range r.Findings {
		m, ok := p.Messages[f.Code]
		if !ok || m.ID != "" && m.ID != f.ID || !repairReadinessID(p, f.ID) || seen[f.ID] || f.Status != m.Status || f.Summary != m.Summary || f.Action != m.Action || f.Objects == nil || len(f.Objects) > p.MaxObjects {
			return false
		}
		seen[f.ID] = true
		for _, object := range f.Objects {
			if object == "" || len(object) > p.MaxTableBytes || !utf8.ValidString(object) || strings.ContainsRune(object, 0) {
				return false
			}
		}
	}
	copy := r
	copy.aggregate()
	raw, err := json.Marshal(r)
	return copy.Status == r.Status && err == nil && len(raw) <= p.MaxResponseBytes
}

// CheckDataRepairCapture leases a writer connection and owns a fresh read-only,
// repeatable-read transaction. It always rolls back and executes only the
// SDK's generated catalog queries; it never invokes installed repair functions.
// Missing prerequisites and connection failures are structured unknown/blocked
// observations. Invalid options return ErrInvalidDataRepairReadiness.
func CheckDataRepairCapture(ctx context.Context, db *sql.DB, options DataRepairCaptureReadinessOptions) (DataRepairCaptureReadinessReport, error) {
	p := repairProbe()
	o, err := normalizeCaptureReadiness(options, p)
	if err != nil {
		return DataRepairCaptureReadinessReport{}, err
	}
	r := DataRepairCaptureReadinessReport{Version: p.Version, Profile: "capture", Scope: o.Scope, Table: o.Table, Release: o.Release, ReceiptProtocol: o.ReceiptProtocol, CheckedAt: time.Now().UTC()}
	for _, id := range p.FindingIDs {
		r.set(p, id, "not_inspected", nil)
	}
	r.set(p, "connection", "writer_unavailable", nil)
	if err := captureReadinessSnapshot(ctx, db, p, o, &r); err != nil {
		r.set(p, "connection", "writer_unavailable", nil)
	}
	r.aggregate()
	if !r.Valid() {
		return DataRepairCaptureReadinessReport{}, ErrInvalidDataRepairReadiness
	}
	return r, nil
}

func captureReadinessSnapshot(ctx context.Context, db *sql.DB, p repairReadinessProgram, o DataRepairCaptureReadinessOptions, r *DataRepairCaptureReadinessReport) (snapshotErr error) {
	if db == nil {
		return ErrInvalidDataRepairReadiness
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.TimeoutMS)*time.Millisecond)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) && snapshotErr == nil {
			snapshotErr = err
		}
	}()
	if _, err := tx.ExecContext(ctx, p.SetupSQL); err != nil {
		return err
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, p.CatalogSQL, o.ReceiptProtocol).Scan(&raw); err != nil {
		return err
	}
	inspect, err := r.frame(p, raw)
	if err != nil || !inspect {
		return err
	}
	if err := tx.QueryRowContext(ctx, p.EnrollmentSQL, o.Table).Scan(&raw); err != nil {
		return err
	}
	_, err = r.frame(p, raw)
	return err
}
