package managedpostgres

import (
	"testing"
	"time"
)

// Hunt 32 H32-4: `postgres usage` reported fresh=true with observed_at from
// a database deleted the month before. A deleted database's final coverage
// stays out of the reported observation time while live databases exist.
func TestLastObservedAtSkipsDeletedDatabases(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	october := time.Date(2026, 10, 12, 15, 2, 38, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		databases []UsageProgress
		want      time.Time
	}{
		{"oldest live database", []UsageProgress{{ObservedAt: now}, {ObservedAt: now.Add(-time.Hour)}, {ObservedAt: october, Terminal: true}}, now.Add(-time.Hour)},
		{"only deleted databases", []UsageProgress{{ObservedAt: october, Terminal: true}, {ObservedAt: october.Add(time.Hour), Terminal: true}}, october.Add(time.Hour)},
		{"live database not yet observed", []UsageProgress{{}, {ObservedAt: now}, {ObservedAt: october, Terminal: true}}, time.Time{}},
		{"no databases", nil, time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := lastObservedAt(tc.databases); !got.Equal(tc.want) {
				t.Fatalf("lastObservedAt = %s, want %s", got, tc.want)
			}
		})
	}
}
