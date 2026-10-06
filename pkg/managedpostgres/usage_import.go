package managedpostgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// These are operator request bounds, not customer entitlements.
const MaximumUsageImportWindows = 256

type UsageImportWindow struct {
	From       time.Time      `json:"from"`
	To         time.Time      `json:"to"`
	ObservedAt time.Time      `json:"observed_at"`
	Readings   []MeterReading `json:"readings"`
}

type UsageImportRequest struct {
	ImportID          string              `json:"import_id"`
	DatabaseID        string              `json:"database_id"`
	EvidenceReference string              `json:"evidence_reference"`
	EvidenceSHA256    string              `json:"evidence_sha256"`
	Reason            string              `json:"reason"`
	Windows           []UsageImportWindow `json:"windows"`
	ExpectedRevision  string              `json:"expected_revision,omitempty"`
}

type UsageImportResult struct {
	ImportID               string    `json:"import_id"`
	DatabaseID             string    `json:"database_id"`
	Revision               string    `json:"revision"`
	Applied                bool      `json:"applied"`
	WindowCount            int       `json:"window_count"`
	PreviousCostMillicents int64     `json:"previous_cost_millicents"`
	ImportedCostMillicents int64     `json:"imported_cost_millicents"`
	CostDeltaMillicents    int64     `json:"cost_delta_millicents"`
	CollectedFrom          time.Time `json:"collected_from"`
	CollectedUntil         time.Time `json:"collected_until"`
	ObservedAt             time.Time `json:"observed_at"`
}

// UsageImportCommand is prepared from the server's catalog and policy. Stores
// serialize it with collectors and retain the before/after records in one commit.
type UsageImportCommand struct {
	Request  UsageImportRequest
	ActorID  string
	Expected Database
	Records  [][]UsageRecord
	Policy   UsagePolicy
	Now      time.Time
}

type UsageImportStore interface {
	ImportUsage(context.Context, UsageImportCommand, bool) (UsageImportResult, error)
	ReplayUsageImport(context.Context, string, string, UsageImportRequest) (UsageImportResult, error)
}

func (s *Service) ImportUsage(ctx context.Context, accountID, actorID string, request UsageImportRequest, apply bool) (UsageImportResult, error) {
	store, ok := s.store.(UsageImportStore)
	policy := s.registry.UsagePolicy()
	if !ok {
		return UsageImportResult{}, ErrUnsupported
	}
	now := s.now().UTC()
	if err := validateUsageImportRequest(request, actorID, now, apply); err != nil {
		return UsageImportResult{}, err
	}
	importID, _ := uuid.Parse(request.ImportID)
	databaseID, _ := uuid.Parse(request.DatabaseID)
	request.ImportID, request.DatabaseID = importID.String(), databaseID.String()
	if apply {
		result, err := store.ReplayUsageImport(ctx, accountID, actorID, request)
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return UsageImportResult{}, err
		}
	}
	if !policy.Enabled {
		return UsageImportResult{}, ErrUnsupported
	}
	database, err := s.store.Get(ctx, accountID, request.DatabaseID)
	if err != nil {
		return UsageImportResult{}, err
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return UsageImportResult{}, err
	}
	if len(backend.Capabilities.UsageMeters) == 0 || (database.RestoreSourceDatabaseID != "" && backend.Capabilities.RestoreUsageIncludedInSource) {
		return UsageImportResult{}, ErrConflict
	}
	command := UsageImportCommand{Request: request, ActorID: actorID, Expected: database, Now: now, Policy: policy}
	for _, window := range request.Windows {
		if window.To.Sub(window.From) != policy.Window || len(window.Readings) != len(backend.Capabilities.UsageMeters) {
			return UsageImportResult{}, ErrInvalid
		}
		var records []UsageRecord
		for _, reading := range window.Readings {
			if !contains(backend.Capabilities.UsageMeters, reading.Meter) {
				return UsageImportResult{}, ErrInvalid
			}
			cost, err := policy.Cost(reading)
			if err != nil {
				return UsageImportResult{}, err
			}
			records = append(records, UsageRecord{AccountID: accountID, DatabaseID: database.ID,
				BackendID: database.BackendID, BackendFingerprint: database.BackendFingerprint,
				WindowFrom: window.From.UTC(), WindowTo: window.To.UTC(), ObservedAt: window.ObservedAt.UTC(),
				Meter: reading.Meter, Quantity: reading.Quantity, CostMillicents: cost})
		}
		sort.Slice(records, func(i, j int) bool { return records[i].Meter < records[j].Meter })
		command.Records = append(command.Records, records)
	}
	return store.ImportUsage(ctx, command, apply)
}

func validateUsageImportRequest(r UsageImportRequest, actor string, now time.Time, apply bool) error {
	if _, err := uuid.Parse(r.ImportID); err != nil {
		return ErrInvalid
	}
	if _, err := uuid.Parse(r.DatabaseID); err != nil {
		return ErrInvalid
	}
	if actor == "" || len(actor) > 256 || now.IsZero() || !validSHA256(r.EvidenceSHA256) ||
		!validImportText(r.EvidenceReference, 256) || !validImportText(r.Reason, 512) ||
		len(r.Windows) == 0 || len(r.Windows) > MaximumUsageImportWindows ||
		(apply && !validSHA256(r.ExpectedRevision)) || (!apply && r.ExpectedRevision != "") {
		return ErrInvalid
	}
	for i, w := range r.Windows {
		if w.From.IsZero() || !w.To.After(w.From) || w.ObservedAt.Before(w.To) || w.ObservedAt.After(now) ||
			w.From.Nanosecond()%1000 != 0 || w.To.Nanosecond()%1000 != 0 || w.ObservedAt.Nanosecond()%1000 != 0 ||
			len(w.Readings) == 0 || len(w.Readings) > 6 || (i > 0 && !r.Windows[i-1].To.Equal(w.From)) {
			return ErrInvalid
		}
	}
	return nil
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func validImportText(value string, limit int) bool {
	return len(value) > 0 && len(value) <= limit && strings.TrimSpace(value) == value &&
		!strings.ContainsFunc(value, unicode.IsControl)
}

func importHash(value any) string {
	data, _ := json.Marshal(value) // All callers use finite, JSON-compatible value types.
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func usageImportRequestHash(actor string, request UsageImportRequest) string {
	return importHash(struct {
		Actor   string
		Request UsageImportRequest
	}{actor, request})
}
