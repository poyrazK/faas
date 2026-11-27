// Package dbmigration evaluates the prerequisites for a native logical migration.
package dbmigration

import (
	"regexp"
	"sort"
)

const (
	SourceMajor         = 16
	MaxRetentionBytes   = 1 << 30
	MinReplicationSlots = 3
	MaxReportedTables   = 16
)

type Source struct {
	Major             int      `json:"major"`
	Writable          bool     `json:"writable"`
	LogicalWAL        bool     `json:"logical_wal"`
	CanReplicate      bool     `json:"can_replicate"`
	CanPublish        bool     `json:"can_publish"`
	FreeSlots         int64    `json:"free_slots"`
	FreeSenders       int64    `json:"free_senders"`
	RetentionBytes    int64    `json:"retention_bytes"`
	DatabaseBytes     int64    `json:"database_bytes"`
	Tables            int64    `json:"tables"`
	Sequences         int64    `json:"sequences"`
	LargeObjects      bool     `json:"large_objects"`
	UnsupportedCount  int64    `json:"unsupported_count"`
	UnsupportedTables []string `json:"unsupported_tables"`
}

type Target struct {
	Major               int
	Empty               bool
	FreeBytes           int64
	StorageReserveBytes int64
	HasWorkerCapacity   bool
}

// Check reports compatibility evidence from one inspection. It does not
// represent a subscription, a data copy, or a cutover decision.
type Check struct {
	Outcome               string   `json:"outcome"`
	Reasons               []string `json:"reasons,omitempty"`
	SourceMajor           int      `json:"source_major,omitempty"`
	TargetMajor           int      `json:"target_major,omitempty"`
	TableCount            int64    `json:"table_count"`
	SequenceCount         int64    `json:"sequence_count"`
	UnsupportedTableCount int64    `json:"unsupported_table_count,omitempty"`
	UnsupportedTables     []string `json:"unsupported_tables,omitempty"`
	SourceBytes           int64    `json:"source_bytes,omitempty"`
	TargetFreeBytes       int64    `json:"target_free_bytes,omitempty"`
	SourceWALLimitBytes   int64    `json:"source_wal_limit_bytes"`
	FreeSourceSlots       int64    `json:"free_source_slots"`
	FreeSourceSenders     int64    `json:"free_source_senders"`
}

const (
	Compatible = "compatible"
	Blocked    = "blocked"
	Unverified = "unverified"
)

// Assess includes every visible blocker so customers can address them in one pass.
func Assess(source Source, target Target) Check {
	out := Check{
		Outcome: Compatible, SourceMajor: source.Major, TargetMajor: target.Major,
		TableCount: max(source.Tables, 0), SequenceCount: max(source.Sequences, 0),
		UnsupportedTableCount: max(source.UnsupportedCount, 0), UnsupportedTables: ReportableTables(source.UnsupportedTables),
		SourceBytes: max(source.DatabaseBytes, 0), TargetFreeBytes: max(target.FreeBytes, 0),
		SourceWALLimitBytes: source.RetentionBytes,
		FreeSourceSlots:     max(source.FreeSlots, 0), FreeSourceSenders: max(source.FreeSenders, 0),
	}
	requirements := []struct {
		ok   bool
		code string
	}{
		{source.Major == SourceMajor, "source_postgres_16_required"},
		{source.Writable, "source_writable_required"},
		{source.LogicalWAL, "source_logical_replication_required"},
		{source.CanReplicate, "source_replication_permission_required"},
		{source.CanPublish, "source_publication_permission_required"},
		{source.FreeSlots >= MinReplicationSlots, "source_replication_slots_capacity"},
		{source.FreeSenders >= MinReplicationSlots, "source_wal_senders_capacity"},
		{source.RetentionBytes > 0 && source.RetentionBytes <= MaxRetentionBytes, "source_wal_retention_limit_required"},
		{source.Tables > 0, "source_tables_required"},
		{source.Tables <= MaxTables, "source_table_limit_exceeded"},
		{source.UnsupportedCount == 0, "source_tables_unsupported"},
		{!source.LargeObjects, "source_large_objects_unsupported"},
		{target.Major >= SourceMajor, "target_postgres_too_old"},
		{target.HasWorkerCapacity, "target_replication_workers_capacity"},
		{target.Empty, "target_not_empty"},
		{source.DatabaseBytes > 0 && target.FreeBytes >= target.StorageReserveBytes &&
			source.DatabaseBytes <= target.FreeBytes-target.StorageReserveBytes, "target_storage_insufficient"},
	}
	for _, requirement := range requirements {
		if !requirement.ok {
			out.Outcome = Blocked
			out.Reasons = append(out.Reasons, requirement.code)
		}
	}
	return out
}

var safeTable = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}\.[a-z_][a-z0-9_]{0,62}$`)

// ReportableTables bounds labels from untrusted source servers. Counts
// remain authoritative when identifier names are omitted from the report.
func ReportableTables(names []string) []string {
	var out []string
	for _, name := range names {
		if len(out) == MaxReportedTables {
			break
		}
		if safeTable.MatchString(name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
