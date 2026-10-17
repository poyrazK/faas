// Package connectioncapacity owns private, measured PostgreSQL capacity
// evidence. Reservations are durable allocations, never expiring worker leases.
package connectioncapacity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrUnknown      = errors.New("database capacity needs qualification")
	ErrInsufficient = errors.New("insufficient database capacity")
)

type Limits struct {
	Connections int64 `toml:"connections"`
	Slots       int64 `toml:"replication_slots"`
	Senders     int64 `toml:"wal_senders"`
	Workers     int64 `toml:"logical_workers"`
}

// Qualification is an operator-reviewed allocation, not a capacity inferred
// from compute class or max_connections. Scope groups ALL aliases/databases
// sharing physical limits. Placement pins a provider resource/generation.
type Qualification struct {
	Scope              string    `toml:"scope"`
	Endpoints          []string  `toml:"source_endpoints"`
	DatabaseID         string    `toml:"database_id"`
	BackendFingerprint string    `toml:"backend_fingerprint"`
	DataResourceID     string    `toml:"data_resource_id"`
	DatabaseGeneration int64     `toml:"database_generation"`
	Placement          string    `toml:"placement"`
	Generation         int64     `toml:"generation"`
	ReportSHA256       string    `toml:"report_sha256"`
	QualifiedAt        time.Time `toml:"qualified_at"`
	ExpiresAt          time.Time `toml:"expires_at"`
	Allocation         Limits    `toml:"allocation"`
	External           Limits    `toml:"external"`
}

func Fingerprint(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (q Qualification) Valid(now time.Time) bool {
	_, err := hex.DecodeString(q.ReportSHA256)
	if err != nil || len(q.ReportSHA256) != sha256.Size*2 || q.Scope == "" || len(q.Scope) > 256 || q.Placement == "" || len(q.Placement) > 256 || q.Generation < 1 || q.QualifiedAt.IsZero() || now.Before(q.QualifiedAt) || !now.Before(q.ExpiresAt) || q.ExpiresAt.Sub(q.QualifiedAt) > api.DataPipelineQualificationMaxAge {
		return false
	}
	for _, n := range []int64{q.Allocation.Connections, q.Allocation.Slots, q.Allocation.Senders, q.Allocation.Workers, q.External.Connections, q.External.Slots, q.External.Senders, q.External.Workers} {
		if n < 0 || n > api.DataAPIConnectionBudgetMaxCount {
			return false
		}
	}
	return q.Allocation.Connections > 0 && q.External.Connections >= api.DataPipelineControlConnections
}

type Evidence struct {
	Key, Placement, ReportSHA256 string
	Generation                   int64
	Limits
	ObservedAt, ExpiresAt time.Time
}

type Snapshot struct {
	Evidence
	Reserved Limits
}

func (s Snapshot) Fresh(now time.Time) bool {
	return !s.ObservedAt.IsZero() && !now.Before(s.ObservedAt) && now.Before(s.ExpiresAt) && s.ExpiresAt.Sub(s.ObservedAt) <= api.DataPipelineCapacityMaxAge
}

func (s Snapshot) Fits(d Limits) bool {
	return s.Reserved.Connections+d.Connections <= s.Connections && s.Reserved.Slots+d.Slots <= s.Slots && s.Reserved.Senders+d.Senders <= s.Senders && s.Reserved.Workers+d.Workers <= s.Workers
}

func PipelineSourceDemand() Limits {
	return Limits{Connections: api.DataPipelineControlConnections + api.DataPipelineCopyWorkersMax + 1, Slots: api.DataPipelineCopyWorkersMax + 1, Senders: api.DataPipelineCopyWorkersMax + 1}
}
func PipelineTargetDemand() Limits {
	return Limits{Connections: api.DataPipelineControlConnections + api.DataPipelineCopyWorkersMax + 1, Workers: api.DataPipelineCopyWorkersMax + 1}
}
