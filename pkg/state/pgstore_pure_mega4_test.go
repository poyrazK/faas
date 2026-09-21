// pgstore_pure_mega4_test.go — Coverage Mega-PR #4 cluster 2:
// fill pkg/state coverage on the pure helpers in pkg/state/pgstore.go
// + the standalone pure helpers in their own files
// (heartbeat_gap.go, machine.go, keys.go, min_instances_effective_*.go,
//  adapter_apid_pgtype.go).
//
// Targets (baseline 43.2% on pkg/state at branch time, post-cluster-1):
//   - Sha256Equal, RedistributeTraffic
//   - ptr/deref/nullable/json helpers (~25 small wrappers)
//   - ClassifyHeartbeatGap (heartbeat_gap.go:81)
//   - State.{Valid,CanTransition,CountsForConcurrency,CountsForRAM,IsLive} (machine.go)
//   - SnapMemKey / SnapVMStateKey / WarmSnapMemKey / WarmSnapVMStateKey (keys.go)
//   - effectiveMinInstances / effectiveDeploymentMinInstances
//   - NewPgtypeUUID{,Ptr} / NewPgtypeTime (adapter_apid_pgtype.go)
//
// Whitebox `package state`.

package state

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
)

// --- Sha256Equal ─────────────────────────────────────────────────

func TestSha256Equal_Mega4(t *testing.T) {
	t.Parallel()
	a := []byte("recovery-code-1")
	b := []byte("recovery-code-1")
	c := []byte("recovery-code-2")
	d := []byte("recovery-code-") // same prefix as a, last byte differs

	if !Sha256Equal(a, b) {
		t.Error("Sha256Equal(a, a): want true")
	}
	if Sha256Equal(a, c) {
		t.Error("Sha256Equal(a, c): want false (different content)")
	}
	if Sha256Equal(a, d) {
		t.Error("Sha256Equal(a, d): want false (different length)")
	}
	if !Sha256Equal([]byte{}, []byte{}) {
		t.Error("Sha256Equal(empty, empty): want true")
	}
	if Sha256Equal([]byte{}, []byte{0}) {
		t.Error("Sha256Equal(empty, single byte): want false")
	}
}

// --- RedistributeTraffic ─────────────────────────────────────────

func TestRedistributeTraffic_Mega4(t *testing.T) {
	t.Parallel()

	siblings := []struct {
		ID    string
		Prior int
	}{}

	t.Run("empty siblings returns nil", func(t *testing.T) {
		t.Parallel()
		if got := RedistributeTraffic(siblings, 100); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("residual<=0 zeros every sibling", func(t *testing.T) {
		t.Parallel()
		s := []struct {
			ID    string
			Prior int
		}{{ID: "a", Prior: 60}, {ID: "b", Prior: 40}}
		got := RedistributeTraffic(s, 0)
		if len(got) != 2 || got[0] != 0 || got[1] != 0 {
			t.Errorf("residual=0: got %v, want [0,0]", got)
		}
	})

	t.Run("sumPrior<=0 distributes evenly by ID-ASC tie-break", func(t *testing.T) {
		t.Parallel()
		// residual=10, n=3, base=3, mod=1 → ID-ASC order is "a","b","c";
		// input order is c,a,b → ID-ASC index order is [1,2,0]; +1 lands on
		// out[1] (the "a" sibling). Expected: [3,4,3].
		s := []struct {
			ID    string
			Prior int
		}{{ID: "c", Prior: 0}, {ID: "a", Prior: 0}, {ID: "b", Prior: 0}}
		got := RedistributeTraffic(s, 10)
		if len(got) != 3 {
			t.Fatalf("len = %d, want 3", len(got))
		}
		if got[0] != 3 {
			t.Errorf("got[0] (c) = %d, want 3", got[0])
		}
		if got[1] != 4 {
			t.Errorf("got[1] (a) = %d, want 4 (ID-ASC first absorbs +1)", got[1])
		}
		if got[2] != 3 {
			t.Errorf("got[2] (b) = %d, want 3", got[2])
		}
	})

	t.Run("weighted split", func(t *testing.T) {
		t.Parallel()
		s := []struct {
			ID    string
			Prior int
		}{{ID: "a", Prior: 30}, {ID: "b", Prior: 70}}
		got := RedistributeTraffic(s, 100)
		if got[0] != 30 || got[1] != 70 {
			t.Errorf("weighted: got %v, want [30,70]", got)
		}
	})

	t.Run("single sibling absorbs everything", func(t *testing.T) {
		t.Parallel()
		s := []struct {
			ID    string
			Prior int
		}{{ID: "a", Prior: 100}}
		got := RedistributeTraffic(s, 100)
		if got[0] != 100 {
			t.Errorf("single: got %v, want [100]", got)
		}
	})
}

// --- ptr / deref / nullable helpers ──────────────────────────────

func TestDerefString_DerefStrings_DerefTime_PtrTime_Mega4(t *testing.T) {
	t.Parallel()

	s := "hello"
	if derefString(&s) != "hello" {
		t.Errorf("derefString(&s): got %q", derefString(&s))
	}
	if derefString(nil) != "" {
		t.Errorf("derefString(nil): got %q, want empty", derefString(nil))
	}

	slice := []string{"a", "b"}
	if got := derefStrings(&slice); len(got) != 2 || got[0] != "a" {
		t.Errorf("derefStrings(&slice): got %v", got)
	}
	if derefStrings(nil) != nil {
		t.Error("derefStrings(nil): got non-nil")
	}

	now := time.Now().UTC()
	if derefTime(&now).IsZero() {
		t.Error("derefTime(&now): zero, want non-zero")
	}
	if !derefTime(nil).IsZero() {
		t.Error("derefTime(nil): non-zero, want zero time")
	}
	if ptrTime(now) == nil {
		t.Error("ptrTime(now): nil")
	}
	if ptrTime(time.Time{}) != nil {
		t.Error("ptrTime(zero): non-nil")
	}
}

func TestNullableStr_NullableTime_NilToEmpty_JsonOrEmpty_NullableJSON_Mega4(t *testing.T) {
	t.Parallel()

	if nullableStr("") != nil {
		t.Error("nullableStr(\"\"): non-nil")
	}
	if nullableStr("x") != "x" {
		t.Error("nullableStr(\"x\"): != \"x\"")
	}

	zero := time.Time{}
	if nullableTime(zero) != nil {
		t.Error("nullableTime(zero): non-nil")
	}
	if nullableTime(time.Unix(1, 0)) == nil {
		t.Error("nullableTime(non-zero): nil")
	}

	if got := nilToEmpty(nil); got == nil {
		t.Error("nilToEmpty(nil): nil, want empty slice (coalesced)")
	}
	if got := nilToEmpty([]string{}); len(got) != 0 {
		t.Errorf("nilToEmpty([]): got %v", got)
	}

	if _, err := jsonOrEmpty(nil); err != nil {
		t.Errorf("jsonOrEmpty(nil): %v", err)
	}
	if _, err := jsonOrEmpty(json.RawMessage(`{"a":1}`)); err != nil {
		t.Errorf("jsonOrEmpty(valid): %v", err)
	}
	if nullableJSON(nil) != nil {
		t.Error("nullableJSON(nil): non-nil")
	}
	if nullableJSON(json.RawMessage(`{}`)) == nil {
		t.Error("nullableJSON({}): nil")
	}
}

func TestNullString_NullableInt_NullAppStatus_NullJSONRaw_NotNullEmptyJSONRaw_NullableOverridePort_Mega4(t *testing.T) {
	t.Parallel()

	if nullString("") != nil {
		t.Error("nullString(\"\"): non-nil")
	}
	if nullString("x") != "x" {
		t.Error("nullString(\"x\"): != \"x\"")
	}

	if nullableInt(0) != nil {
		t.Error("nullableInt(0): non-nil (0 must map to NULL)")
	}
	if nullableInt(7) != 7 {
		t.Error("nullableInt(7): != 7")
	}

	if nullAppStatus(nil) != nil {
		t.Error("nullAppStatus(nil): non-nil")
	}
	status := AppStatus("active")
	if nullAppStatus(&status) == nil {
		t.Error("nullAppStatus(&status): nil")
	}

	if nullJSONRaw(nil) != nil {
		t.Error("nullJSONRaw(nil): non-nil")
	}
	if nullJSONRaw(json.RawMessage(`{}`)) == nil {
		t.Error("nullJSONRaw({}): nil")
	}
	// notNullEmptyJSONRaw: {} stays a non-NULL JSON '{}' (NOT "NULL").
	got := notNullEmptyJSONRaw(json.RawMessage(`{}`))
	if got == nil {
		t.Error("notNullEmptyJSONRaw({}): nil")
	}
	// Empty / nil → wire-shape "[]" (pgx text-protocol coerces to jsonb `[]`).
	if s, ok := notNullEmptyJSONRaw(nil).(string); !ok || s != "[]" {
		t.Errorf("notNullEmptyJSONRaw(nil): got %v (%T), want string %q", notNullEmptyJSONRaw(nil), notNullEmptyJSONRaw(nil), "[]")
	}
	if s, ok := notNullEmptyJSONRaw(json.RawMessage(``)).(string); !ok || s != "[]" {
		t.Errorf("notNullEmptyJSONRaw(empty): got %v, want %q", notNullEmptyJSONRaw(json.RawMessage(``)), "[]")
	}

	if nullableOverridePort(0) != nil {
		t.Error("nullableOverridePort(0): non-nil")
	}
	if nullableOverridePort(443) != 443 {
		t.Error("nullableOverridePort(443): != 443")
	}
}

func TestLogExcerptsJSON_CidrPrefixesToArray_CidrTextToPrefixes_Mega4(t *testing.T) {
	t.Parallel()

	logs := []api.LogExcerpt{{Timestamp: "2026-08-23T12:00:00Z", Message: "hi"}}
	if logExcerptsJSON(nil) != nil {
		t.Error("logExcerptsJSON(nil): non-nil")
	}
	if logExcerptsJSON(logs) == nil {
		t.Error("logExcerptsJSON(populated): nil")
	}

	// CIDR helpers round-trip.
	in := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("192.168.0.0/16"),
	}
	arr := cidrPrefixesToArray(in)
	if !strings.Contains(arr, "10.0.0.0/8") || !strings.Contains(arr, "192.168.0.0/16") {
		t.Errorf("cidrPrefixesToArray: %q missing expected entries", arr)
	}

	out := cidrTextToPrefixes(arr)
	if len(out) != 2 {
		t.Errorf("cidrTextToPrefixes len = %d, want 2", len(out))
	}

	// Empty input round-trip.
	if got := cidrPrefixesToArray(nil); got != "{}" {
		t.Errorf("cidrPrefixesToArray(nil): got %q, want %q", got, "{}")
	}
	if got := cidrTextToPrefixes(""); len(got) != 0 {
		t.Errorf("cidrTextToPrefixes(\"\"): got %v, want empty", got)
	}
	// Symmetric: "{}" round-trips back to nil.
	if got := cidrTextToPrefixes("{}"); len(got) != 0 {
		t.Errorf("cidrTextToPrefixes({}): got %v, want empty", got)
	}
}

func TestNullableTimestamptz_NullableTimestamptzPtr_NullIfEmpty_Mega4(t *testing.T) {
	t.Parallel()

	got := nullableTimestamptz(time.Time{})
	if got.Valid {
		t.Error("nullableTimestamptz(zero): Valid=true, want false")
	}
	got2 := nullableTimestamptz(time.Unix(1700000000, 0).UTC())
	if !got2.Valid {
		t.Error("nullableTimestamptz(non-zero): Valid=false")
	}

	if nullableTimestamptzPtr(nil).Valid {
		t.Error("nullableTimestamptzPtr(nil): Valid=true")
	}
	now := time.Unix(1700000000, 0).UTC()
	if !nullableTimestamptzPtr(&now).Valid {
		t.Error("nullableTimestamptzPtr(&now): Valid=false")
	}

	if nullIfEmpty("") != nil {
		t.Error("nullIfEmpty(\"\"): non-nil")
	}
	if nullIfEmpty("x") != "x" {
		t.Error("nullIfEmpty(\"x\"): != \"x\"")
	}
}

// --- pgtype / uuid adapters ──────────────────────────────────────

func TestUUIDFromPgtype_PgtypeFromUUID_PgtypeFromTime_Mega4(t *testing.T) {
	t.Parallel()

	u := uuid.MustParse("12345678-1234-5678-9abc-123456789012")
	pg := pgtypeFromUUID(u)
	if !pg.Valid {
		t.Fatal("pgtypeFromUUID: Valid=false")
	}
	roundTrip := uuidFromPgtype(pg)
	if roundTrip != u {
		t.Errorf("uuidFromPgtype round-trip = %v, want %v", roundTrip, u)
	}

	// Invalid → uuid.Nil
	inv := pgtype.UUID{Valid: false}
	if uuidFromPgtype(inv) != uuid.Nil {
		t.Errorf("uuidFromPgtype(invalid): %v, want uuid.Nil", uuidFromPgtype(inv))
	}

	now := time.Unix(1700000000, 0).UTC()
	pgTime := pgtypeFromTime(now)
	if !pgTime.Valid {
		t.Error("pgtypeFromTime: Valid=false")
	}
	if !pgTime.Time.Equal(now) {
		t.Errorf("pgtypeFromTime.Time = %v, want %v", pgTime.Time, now)
	}
}

// --- ClassifyHeartbeatGap ────────────────────────────────────────

func TestClassifyHeartbeatGap_Mega4(t *testing.T) {
	t.Parallel()

	interval := 30 * time.Second
	staleness := 5 * time.Minute
	prev := time.Unix(1_700_000_000, 0).UTC()

	cases := []struct {
		name      string
		gap       time.Duration
		wantMiss  bool
		wantStale bool
	}{
		{"under interval (no flag)", 10 * time.Second, false, false},
		{"just over interval (missed)", 31 * time.Second, true, false},
		{"up to staleness (missed only)", 4 * time.Minute, true, false},
		{"beyond staleness (missed + stale)", 6 * time.Minute, true, true},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			curr := prev.Add(c.gap)
			sum := ClassifyHeartbeatGap(prev, curr, interval, staleness)
			if sum.Gap != c.gap {
				t.Errorf("Gap = %v, want %v", sum.Gap, c.gap)
			}
			if sum.Missed != c.wantMiss {
				t.Errorf("Missed = %v, want %v", sum.Missed, c.wantMiss)
			}
			if sum.Stale != c.wantStale {
				t.Errorf("Stale = %v, want %v", sum.Stale, c.wantStale)
			}
		})
	}

	// Zero prev → returns Gap with no flags.
	t.Run("zero prev", func(t *testing.T) {
		t.Parallel()
		sum := ClassifyHeartbeatGap(time.Time{}, time.Unix(1700000000, 0), interval, staleness)
		if sum.Missed || sum.Stale {
			t.Errorf("zero prev: flags set, want both false")
		}
	})
}

// --- State machine ───────────────────────────────────────────────

func TestStateMachineMethods_Mega4(t *testing.T) {
	t.Parallel()

	// IsLive is a package-level function that wraps CountsForRAM.
	if !IsLive("running") {
		t.Error("IsLive(running): false, want true")
	}
	if IsLive("parked") {
		t.Error("IsLive(parked): true, want false")
	}
	if !State("cold_booting").CountsForConcurrency() {
		t.Error("cold_booting.CountsForConcurrency(): false, want true")
	}

	// CanTransition: at least the cold-boot → running edge.
	if !CanTransition("cold_booting", "running") {
		t.Error("cold_booting → running: false, want true")
	}
	if CanTransition("parked", "running") {
		t.Error("parked → running: true, want false (must wake first)")
	}
}

// --- Snap keys ───────────────────────────────────────────────────

func TestSnapKeys_Mega4(t *testing.T) {
	t.Parallel()

	depID := "dep-1"
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"SnapMemKey", SnapMemKey(depID), "snap/" + depID + "/mem"},
		{"SnapVMStateKey", SnapVMStateKey(depID), "snap/" + depID + "/vmstate"},
		{"WarmSnapMemKey", WarmSnapMemKey(depID), "snap/" + depID + "/warm/mem"},
		{"WarmSnapVMStateKey", WarmSnapVMStateKey(depID), "snap/" + depID + "/warm/vmstate"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if c.got != c.want {
				t.Errorf("got %q, want %q", c.got, c.want)
			}
		})
	}
}

// --- MinInstances effective ──────────────────────────────────────

func TestEffectiveMinInstances_Mega4(t *testing.T) {
	t.Parallel()

	if got := effectiveMinInstances(nil, time.Now()); got != 0 {
		t.Errorf("nil App: got %d, want 0", got)
	}
	// Legacy column wins when set.
	if got := effectiveMinInstances(&App{MinInstances: 3}, time.Now()); got != 3 {
		t.Errorf("legacy column: got %d, want 3", got)
	}
}

func TestEffectiveDeploymentMinInstances_Mega4(t *testing.T) {
	t.Parallel()

	if got := effectiveDeploymentMinInstances(nil); got != 0 {
		t.Errorf("nil Deployment: got %d, want 0", got)
	}
	if got := effectiveDeploymentMinInstances(&Deployment{MinInstances: -1}); got != 0 {
		t.Errorf("negative floor (clamped to 0): got %d, want 0", got)
	}
	if got := effectiveDeploymentMinInstances(&Deployment{MinInstances: 5}); got != 5 {
		t.Errorf("positive: got %d, want 5", got)
	}
}

// --- adapter_apid_pgtype.NewPgtype* ─────────────────────────────

func TestNewPgtypeUUID_Mega4(t *testing.T) {
	t.Parallel()

	u := uuid.MustParse("12345678-1234-5678-9abc-123456789012")
	got := NewPgtypeUUID(u)
	if !got.Valid {
		t.Fatal("NewPgtypeUUID: Valid=false")
	}

	// nil ptr → invalid
	gotNil := NewPgtypeUUIDPtr(nil)
	if gotNil.Valid {
		t.Error("NewPgtypeUUIDPtr(nil): Valid=true, want false")
	}

	gotPtr := NewPgtypeUUIDPtr(&u)
	if !gotPtr.Valid {
		t.Error("NewPgtypeUUIDPtr(&u): Valid=false")
	}
}

func TestNewPgtypeTime_Mega4(t *testing.T) {
	t.Parallel()

	now := time.Unix(1700000000, 0).UTC()
	got := NewPgtypeTime(now)
	if !got.Valid {
		t.Fatal("NewPgtypeTime: Valid=false")
	}
	if !got.Time.Equal(now) {
		t.Errorf("NewPgtypeTime.Time = %v, want %v", got.Time, now)
	}
}
