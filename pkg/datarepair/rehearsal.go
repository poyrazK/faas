package datarepair

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const MicroVMRunner = "microvm-networkless-postgres16-v1"
const RehearsalVsockPort = 1091
const RehearsalManifestPath = "etc/faas/data-repair.json"

// RunnerSpec refers only to operator-installed immutable platform artifacts.
// The kernel is vmmd's configured kernel. No deployment image is used.
type RunnerSpec struct {
	BaseKey      string `json:"base_key"`
	LayerKey     string `json:"layer_key"`
	KernelSHA256 string `json:"kernel_sha256"`
	BaseSHA256   string `json:"base_sha256"`
	LayerSHA256  string `json:"layer_sha256"`
}

func (s RunnerSpec) Validate() error {
	if s.BaseKey == "" || s.LayerKey == "" || len(s.BaseKey) > 2048 || len(s.LayerKey) > 2048 ||
		!digestPattern.MatchString(s.KernelSHA256) || !digestPattern.MatchString(s.BaseSHA256) || !digestPattern.MatchString(s.LayerSHA256) {
		return ErrInvalid
	}
	return nil
}
func (s RunnerSpec) Digest() string {
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// RehearsalFrame is the durable, payload-free authority for one physical VM.
// It is retained unchanged for cleanup, even when bindings or plans expire.
type RehearsalFrame struct {
	TaskID         string     `json:"task_id"`
	OperationID    string     `json:"operation_id"`
	Scope          Scope      `json:"scope"`
	Binding        string     `json:"binding"`
	NodeID         string     `json:"node_id"`
	Incarnation    string     `json:"incarnation"`
	PlanID         string     `json:"plan_id"`
	PlanDigest     string     `json:"plan_digest"`
	ChecksSHA256   string     `json:"checks_sha256"`
	SnapshotSHA256 string     `json:"snapshot_sha256,omitempty"`
	PlanExpiresAt  time.Time  `json:"plan_expires_at"`
	Deadline       time.Time  `json:"deadline"`
	Plan           api.Plan   `json:"plan"`
	Runner         RunnerSpec `json:"runner"`
}

func (f RehearsalFrame) Validate() error {
	if !validID(f.TaskID) || !validID(f.OperationID) || !validID(f.NodeID) || !validID(f.Incarnation) || f.PlanID != f.OperationID ||
		f.Scope.Validate() != nil || !f.Plan.Valid() || !digestPattern.MatchString(f.Binding) || !digestPattern.MatchString(f.PlanDigest) ||
		!digestPattern.MatchString(f.ChecksSHA256) || f.Runner.Validate() != nil || f.Deadline.IsZero() || f.PlanExpiresAt.IsZero() ||
		f.Deadline.After(f.PlanExpiresAt) || (f.SnapshotSHA256 != "" && !digestPattern.MatchString(f.SnapshotSHA256)) {
		return ErrInvalid
	}
	return nil
}

func (f RehearsalFrame) Same(other RehearsalFrame) bool {
	if !f.Deadline.Equal(other.Deadline) || !f.PlanExpiresAt.Equal(other.PlanExpiresAt) {
		return false
	}
	f.Deadline = other.Deadline
	f.PlanExpiresAt = other.PlanExpiresAt
	return f == other
}

func DecodeRehearsalFrame(b []byte) (RehearsalFrame, error) {
	var f RehearsalFrame
	if len(b) > api.DataRepairMaxRequestBytes {
		return f, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&f) != nil || d.Decode(new(any)) != io.EOF {
		return f, ErrInvalid
	}
	return f, f.Validate()
}

func (f RehearsalFrame) Matches(p Plan) bool {
	return f.PlanID == p.ID && f.PlanDigest == p.Digest && f.ChecksSHA256 == p.ChecksSHA256 && f.Scope == p.Scope && f.PlanExpiresAt.Equal(p.ExpiresAt)
}

// RehearsalInput travels only over authenticated executor/host links and vsock.
// Neither the dump, plan rows, check SQL nor source credentials enter CP rows.
type RehearsalInput struct {
	Plan           Plan    `json:"plan"`
	CheckSQL       string  `json:"check_sql"`
	Checks         []Check `json:"checks,omitempty"`
	Database       string  `json:"database"`
	SnapshotSHA256 string  `json:"snapshot_sha256"`
	Dump           []byte  `json:"-"`
}

func (in RehearsalInput) Validate() error {
	set, err := configuredChecks(in.CheckSQL, in.Checks)
	h := sha256.Sum256(in.Dump)
	if err != nil || !set.matches(in.Plan) || in.Plan.Scope.Validate() != nil || !validID(in.Plan.ID) ||
		!digestPattern.MatchString(in.Plan.Digest) || in.Plan.Candidates < 1 || in.Database == "" || len(in.Database) > 63 ||
		strings.ContainsRune(in.Database, 0) || len(in.Dump) == 0 || len(in.Dump) > api.DataRepairMaxSnapshotBytes ||
		hex.EncodeToString(h[:]) != in.SnapshotSHA256 {
		return ErrInvalid
	}
	return nil
}

// A length-prefixed JSON header followed by raw SQL avoids base64 expansion.
// Both lengths are checked before allocation and trailing bytes are rejected.
func WriteRehearsalInput(w io.Writer, in RehearsalInput) error {
	if err := in.Validate(); err != nil {
		return err
	}
	// Review observations are not execution inputs. Keep the header compatible
	// with existing guest readers; the restored DB retains the original complete
	// document and digest. Copy the items so the reviewed source plan is untouched.
	in.Plan.Items = append([]Item(nil), in.Plan.Items...)
	in.Plan.CheckFailure, in.Plan.ApplyChecks = nil, nil
	in.Plan.Replacement, in.Plan.ReplacementPlanID = nil, ""
	in.Plan.ApplyResolution = nil
	for i := range in.Plan.Items {
		in.Plan.Items[i].Reason = ""
		in.Plan.Items[i].ObservedVersion = ""
		in.Plan.Items[i].ObservedValues = nil
		in.Plan.Items[i].ObservationOmitted = false
		in.Plan.Items[i].ObservedValuesJSON = ""
		in.Plan.Items[i].PreviousStatus = ""
	}
	header, err := json.Marshal(in)
	if err != nil || len(header) > api.DataRepairMaxPlanBytes+api.DataRepairMaxRequestBytes {
		return ErrInvalid
	}
	var lengths [12]byte
	binary.BigEndian.PutUint32(lengths[:4], uint32(len(header)))
	binary.BigEndian.PutUint64(lengths[4:], uint64(len(in.Dump)))
	for _, b := range [][]byte{lengths[:], header, in.Dump} {
		if _, err := w.Write(b); err != nil {
			return err
		}
	}
	return nil
}

func ReadRehearsalInput(r io.Reader) (RehearsalInput, error) {
	in, err := ReadRehearsalBody(r)
	if err != nil {
		return in, err
	}
	var extra [1]byte
	if n, err := r.Read(extra[:]); n != 0 || err != io.EOF {
		return in, ErrInvalid
	}
	return in, nil
}

// ReadRehearsalBody is used by the one-shot vsock exchange, whose result is
// returned on the same connection. Its bounded length is the request boundary.
func ReadRehearsalBody(r io.Reader) (RehearsalInput, error) {
	var in RehearsalInput
	var lengths [12]byte
	if _, err := io.ReadFull(r, lengths[:]); err != nil {
		return in, ErrInvalid
	}
	h, n := binary.BigEndian.Uint32(lengths[:4]), binary.BigEndian.Uint64(lengths[4:])
	if h == 0 || h > api.DataRepairMaxPlanBytes+api.DataRepairMaxRequestBytes || n == 0 || n > api.DataRepairMaxSnapshotBytes {
		return in, ErrInvalid
	}
	header := make([]byte, int(h))
	if _, err := io.ReadFull(r, header); err != nil {
		return in, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(header))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF {
		return in, ErrInvalid
	}
	in.Dump = make([]byte, int(n))
	if _, err := io.ReadFull(r, in.Dump); err != nil {
		return in, ErrInvalid
	}
	return in, in.Validate()
}

type SnapshotExporter struct{ SourceURL, DumpBinary string }

func (x SnapshotExporter) Export(ctx context.Context, p Plan, checks string) (RehearsalInput, error) {
	return x.ExportWithChecks(ctx, p, checks, nil)
}

func (x SnapshotExporter) ExportWithChecks(ctx context.Context, p Plan, legacy string, named []Check) (RehearsalInput, error) {
	set, err := configuredChecks(legacy, named)
	if err != nil || !set.matches(p) || p.PayloadPurgedAt != nil || p.ReplacementPlanID != "" || p.ApplyResolution != nil || p.ApprovedAt != nil || p.AppliedAt != nil || !time.Now().Before(p.ExpiresAt) {
		return RehearsalInput{}, ErrConflict
	}
	dump, database, err := x.snapshot(ctx)
	if err != nil {
		return RehearsalInput{}, err
	}
	h := sha256.Sum256(dump)
	in := RehearsalInput{Plan: p, CheckSQL: set.query, Checks: set.checks, Database: database, SnapshotSHA256: hex.EncodeToString(h[:]), Dump: dump}
	return in, in.Validate()
}

func (e *Engine) RecordRehearsal(ctx context.Context, s Scope, id, digest string, evidence Evidence) (Plan, error) {
	p, err := e.Get(ctx, s, id)
	if err != nil {
		return Plan{}, err
	}
	set, checkErr := configuredChecks(e.CheckSQL, e.Checks)
	if checkErr != nil || !set.matches(p) || p.ReplacementPlanID != "" || p.ApplyResolution != nil || p.Digest != digest || !p.ValidCheckResults(evidence) || evidence.PlanDigest != digest ||
		evidence.ChecksSHA256 != set.digest || !digestPattern.MatchString(evidence.SnapshotSHA256) || evidence.Runner == "" {
		return Plan{}, ErrConflict
	}
	if !evidence.Passed {
		return e.recordCheckFailure(ctx, s, p, "rehearsal", evidence)
	}
	b, _ := json.Marshal(evidence)
	return readPlan(ctx, e.Pool, `SELECT gregale_repair.record_rehearsal($1::uuid,$2::jsonb,$3,$4::jsonb)`, id, scopeJSON(s), digest, b)
}

func (f RehearsalFrame) ValidEvidence(e Evidence) bool {
	return e.Passed && f.ValidOutcome(e)
}

// A negative check report can be durably published after cleanup, but it is
// never successful rehearsal evidence and cannot advance approval.
func (f RehearsalFrame) ValidOutcome(e Evidence) bool {
	return (e.Passed && e.CheckResults == nil || e.CheckResults.Valid(e.Passed)) && e.PlanDigest == f.PlanDigest && e.ChecksSHA256 == f.ChecksSHA256 && e.SnapshotSHA256 == f.SnapshotSHA256 &&
		e.Runner == MicroVMRunner && e.TaskID == f.TaskID && e.NodeID == f.NodeID && e.RunnerSHA256 == f.Runner.Digest() && e.RetiredAt != nil && !e.RetiredAt.IsZero()
}
