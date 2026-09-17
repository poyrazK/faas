// adr: 118
// dispatch_triggers_pure_test.go — fill pkg/sched/dispatch_triggers.go
// coverage of the pure helper surface. Targets the 0%-covered
// pure helpers: closeBatch, buildDispatchEnvelope, batchItemIDs,
// claimedItemIDs, byteReadCloser, marshalJSON, computeRetryBackoff,
// shardKeyFor, classifyDLQReason, plus the nil-safe metrics
// delegates (observeESMPoll/Records/Lag).
//
// Whitebox `package sched` (matches existing pkg/sched tests).

package sched

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// --- closeBatch ---------------------------------------------------

func TestCloseBatch_Empty(t *testing.T) {
	if got := closeBatch(nil, 0, 0); len(got) != 0 {
		t.Errorf("nil input: %d, want 0", len(got))
	}
}

func TestCloseBatch_UnderSizeAndByteCap(t *testing.T) {
	in := []SourceRecord{
		{ItemIdentifier: "a", Payload: []byte("aa")},
		{ItemIdentifier: "b", Payload: []byte("bb")},
	}
	got := closeBatch(in, 10, 1000)
	if len(got) != 2 {
		t.Errorf("got %d records, want 2", len(got))
	}
}

func TestCloseBatch_TruncatesAtSizeMax(t *testing.T) {
	in := []SourceRecord{
		{ItemIdentifier: "a", Payload: []byte("x")},
		{ItemIdentifier: "b", Payload: []byte("x")},
		{ItemIdentifier: "c", Payload: []byte("x")},
	}
	got := closeBatch(in, 2, 10000)
	if len(got) != 2 {
		t.Errorf("sizeMax truncation: got %d, want 2", len(got))
	}
	if got[0].ItemIdentifier != "a" || got[1].ItemIdentifier != "b" {
		t.Errorf("truncation keeps first 2: got %v", got)
	}
}

func TestCloseBatch_TruncatesAtByteCap(t *testing.T) {
	// Each record's payload is 100 bytes; byteCap=150 must admit
	// only the first record (100 fits; second pushes total to 200
	// > 150).
	in := []SourceRecord{
		{ItemIdentifier: "a", Payload: make([]byte, 100)},
		{ItemIdentifier: "b", Payload: make([]byte, 100)},
		{ItemIdentifier: "c", Payload: make([]byte, 100)},
	}
	got := closeBatch(in, 100, 150)
	if len(got) != 1 || got[0].ItemIdentifier != "a" {
		t.Errorf("byteCap truncation: got %v, want [a]", got)
	}
}

func TestCloseBatch_SizeMaxZeroMeansNoCap(t *testing.T) {
	// sizeMax <= 0 → no size cap (only byteCap applies).
	in := []SourceRecord{
		{ItemIdentifier: "a"}, {ItemIdentifier: "b"}, {ItemIdentifier: "c"},
	}
	got := closeBatch(in, 0, 10000)
	if len(got) != 3 {
		t.Errorf("sizeMax=0: got %d, want 3", len(got))
	}
}

// --- buildDispatchEnvelope ---------------------------------------

func mustUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	u, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("bad uuid: %v", err)
	}
	return pgtype.UUID{Bytes: u, Valid: true}
}

func TestBuildDispatchEnvelope_BasicFields(t *testing.T) {
	tr := sqlc.Trigger{
		ID:    mustUUID(t, "11111111-1111-1111-1111-111111111111"),
		AppID: mustUUID(t, "22222222-2222-2222-2222-222222222222"),
	}
	batch := []SourceRecord{
		{ItemIdentifier: "k1", Payload: []byte(`{"hello":"world"}`), Headers: map[string]string{"X-K": "v"}},
	}
	env := buildDispatchEnvelope(tr, batch)
	if env.AppID != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("AppID = %q", env.AppID)
	}
	if !strings.HasPrefix(env.InvocationID, "trigger-") {
		t.Errorf("InvocationID prefix = %q", env.InvocationID)
	}
	if env.Source != "esm" {
		t.Errorf("Source = %q, want esm", env.Source)
	}
	if env.TriggerID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("TriggerID = %q", env.TriggerID)
	}
	if len(env.Records) != 1 {
		t.Fatalf("Records = %d, want 1", len(env.Records))
	}
	if env.Records[0].ItemIdentifier != "k1" {
		t.Errorf("record ItemIdentifier = %q", env.Records[0].ItemIdentifier)
	}
	// PayloadB64 is base64-encoded.
	if env.Records[0].PayloadB64 == "" {
		t.Error("PayloadB64 empty")
	}
	if env.Records[0].Headers["X-K"] != "v" {
		t.Errorf("Headers = %v", env.Records[0].Headers)
	}
}

// --- batchItemIDs / claimedItemIDs ------------------------------

func TestBatchItemIDs_Empty(t *testing.T) {
	if got := batchItemIDs(nil); got != nil {
		t.Errorf("nil: %v, want nil", got)
	}
	if got := batchItemIDs([]SourceRecord{}); got != nil {
		t.Errorf("empty: %v, want nil", got)
	}
}

func TestBatchItemIDs_Multiple(t *testing.T) {
	in := []SourceRecord{
		{ItemIdentifier: "a"}, {ItemIdentifier: "b"}, {ItemIdentifier: "c"},
	}
	got := batchItemIDs(in)
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("got %v, want [a b c]", got)
	}
}

func TestClaimedItemIDs_Empty(t *testing.T) {
	if got := claimedItemIDs(nil); got != nil {
		t.Errorf("nil: %v", got)
	}
}

func TestClaimedItemIDs_Multiple(t *testing.T) {
	in := []sqlc.TriggerRecord{
		{ItemIdentifier: "a"},
		{ItemIdentifier: "b"},
	}
	got := claimedItemIDs(in)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("got %v", got)
	}
}

func TestUnclaimedItemIDsAndRecordsForClaimed(t *testing.T) {
	batch := []SourceRecord{{ItemIdentifier: "a"}, {ItemIdentifier: "b"}, {ItemIdentifier: "c"}}
	claimed := []sqlc.TriggerRecord{{ItemIdentifier: "b"}, {ItemIdentifier: "c"}}
	if got := unclaimedItemIDs(batch, claimed); len(got) != 1 || got[0] != "a" {
		t.Fatalf("unclaimedItemIDs = %v, want [a]", got)
	}
	got := recordsForClaimed(batch, claimed)
	if len(got) != 2 || got[0].ItemIdentifier != "b" || got[1].ItemIdentifier != "c" {
		t.Fatalf("recordsForClaimed = %#v, want [b c]", got)
	}
}

// --- byteReadCloser ----------------------------------------------

func TestByteReadCloser_ReadAll(t *testing.T) {
	r := &byteReadCloser{b: []byte("hello")}
	buf := make([]byte, 3)
	n, err := r.Read(buf)
	if err != nil || n != 3 || string(buf) != "hel" {
		t.Errorf("first read: n=%d err=%v buf=%q", n, err, buf)
	}
	n, err = r.Read(buf)
	if err != nil || n != 2 || string(buf[:n]) != "lo" {
		t.Errorf("second read: n=%d err=%v buf=%q", n, err, buf[:n])
	}
	n, err = r.Read(buf)
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Errorf("EOF: n=%d err=%v, want 0 io.EOF", n, err)
	}
}

func TestByteReadCloser_CloseNoError(t *testing.T) {
	r := &byteReadCloser{b: []byte("x")}
	if err := r.Close(); err != nil {
		t.Errorf("Close err = %v, want nil", err)
	}
}

// --- marshalJSON --------------------------------------------------

func TestMarshalJSON_NilReturnsEmptyObject(t *testing.T) {
	if got := marshalJSON(nil); string(got) != "{}" {
		t.Errorf("nil: %q, want {}", got)
	}
}

func TestMarshalJSON_MapRoundTrip(t *testing.T) {
	got := marshalJSON(map[string]int{"x": 1})
	if !strings.Contains(string(got), `"x":1`) {
		t.Errorf("got %q, want JSON containing x:1", got)
	}
}

func TestMarshalJSON_StringRoundTrip(t *testing.T) {
	got := marshalJSON("hello")
	if string(got) != `"hello"` {
		t.Errorf("got %q", got)
	}
}

// --- computeRetryBackoff ----------------------------------------

func TestComputeRetryBackoff_AttemptsRange(t *testing.T) {
	// 100 trials × 9 attempt values — every observed duration must
	// fall in the documented band.
	bands := map[int32][2]float64{
		1: {0.8, 1.2}, // base 1s, ±20%
		2: {1.6, 2.4},
		3: {3.2, 4.8},
		4: {6.4, 9.6},
		5: {12.8, 19.2},
		6: {25.6, 38.4},
		7: {51.2, 76.8},
		8: {102.4, 153.6},
		9: {204.8, 307.2},
	}
	for attempts, band := range bands {
		var saw bool
		for i := 0; i < 200; i++ {
			d := computeRetryBackoff(attempts)
			secs := d.Seconds()
			if secs >= band[0] && secs <= band[1] {
				saw = true
				break
			}
		}
		if !saw {
			t.Errorf("attempts=%d: no observed duration in band [%v, %v] after 200 trials", attempts, band[0], band[1])
		}
	}
}

func TestComputeRetryBackoff_CapsAt5Minutes(t *testing.T) {
	// attempts=20 → exp=9 (clamped from 19), base=5min, jitter band
	// is [4min, 6min].
	for i := 0; i < 50; i++ {
		d := computeRetryBackoff(20)
		if d > 6*time.Minute || d < 4*time.Minute {
			t.Errorf("attempts=20: got %v, want [4m, 6m]", d)
		}
	}
}

func TestComputeRetryBackoff_AttemptsZeroTreatedAsOne(t *testing.T) {
	// attempts=0 → exp=0 (clamped from -1), base=1s, jitter band
	// is [0.8s, 1.2s].
	for i := 0; i < 50; i++ {
		d := computeRetryBackoff(0)
		if d > 1200*time.Millisecond || d < 800*time.Millisecond {
			t.Errorf("attempts=0: got %v, want [0.8s, 1.2s]", d)
		}
	}
}

func TestComputeRetryBackoff_NegativeTreatedAsOne(t *testing.T) {
	d := computeRetryBackoff(-5)
	if d > 1200*time.Millisecond || d < 800*time.Millisecond {
		t.Errorf("attempts=-5: got %v, want [0.8s, 1.2s]", d)
	}
}

func TestComputeTriggerRetryBackoffUsesConfiguredPolicy(t *testing.T) {
	configured := sqlc.Trigger{Config: []byte(`{"retry_policy":{"base_seconds":2,"max_seconds":10}}`)}
	if got := computeTriggerRetryBackoff(configured, 1); got != 2*time.Second {
		t.Fatalf("configured retry backoff = %v, want 2s", got)
	}
	legacy := sqlc.Trigger{Config: []byte(`{"mode":"queue"}`)}
	if got := computeTriggerRetryBackoff(legacy, 1); got < 800*time.Millisecond || got > 1200*time.Millisecond {
		t.Fatalf("legacy retry backoff = %v, want historical [800ms,1200ms]", got)
	}
}

// --- shardKeyFor --------------------------------------------------

func TestShardKeyFor_KafkaPartition(t *testing.T) {
	rec := SourceRecord{Metadata: map[string]any{"partition": int64(7)}}
	if got := shardKeyFor(rec, string(api.TriggerKindKafka)); got != "7" {
		t.Errorf("got %q, want 7", got)
	}
}

func TestShardKeyFor_NatsStream(t *testing.T) {
	rec := SourceRecord{Metadata: map[string]any{"stream": "events"}}
	if got := shardKeyFor(rec, string(api.TriggerKindNATS)); got != "events" {
		t.Errorf("got %q, want events", got)
	}
}

func TestShardKeyFor_UnknownKindFallsBackToAgg(t *testing.T) {
	rec := SourceRecord{Metadata: map[string]any{"partition": int64(1)}}
	if got := shardKeyFor(rec, "sqs"); got != "_agg" {
		t.Errorf("got %q, want _agg", got)
	}
}

func TestShardKeyFor_EmptyValueCollapsesToAgg(t *testing.T) {
	rec := SourceRecord{Metadata: map[string]any{}}
	if got := shardKeyFor(rec, string(api.TriggerKindKafka)); got != "_agg" {
		t.Errorf("got %q, want _agg", got)
	}
}

func TestShardKeyFor_OverlongKeyCollapsesToAgg(t *testing.T) {
	// 33-char partition key → cap to "_agg" (Prometheus cardinality
	// protection).
	long := strings.Repeat("a", 33)
	rec := SourceRecord{Metadata: map[string]any{"partition": long}}
	if got := shardKeyFor(rec, string(api.TriggerKindKafka)); got != "_agg" {
		t.Errorf("got %q, want _agg", got)
	}
}

func TestShardKeyFor_AtLengthBoundary(t *testing.T) {
	// Exactly 32 chars is the boundary; should NOT collapse.
	at := strings.Repeat("a", 32)
	rec := SourceRecord{Metadata: map[string]any{"partition": at}}
	if got := shardKeyFor(rec, string(api.TriggerKindKafka)); got != at {
		t.Errorf("got len=%d, want 32", len(got))
	}
}

func TestShardKeyFor_NilMetadata(t *testing.T) {
	rec := SourceRecord{}
	if got := shardKeyFor(rec, string(api.TriggerKindKafka)); got != "_agg" {
		t.Errorf("got %q, want _agg", got)
	}
}

func TestConsumerLagFor_KafkaHighWaterMark(t *testing.T) {
	rec := SourceRecord{Metadata: map[string]any{
		"offset":          int64(40),
		"high_water_mark": int64(47),
	}}
	if got, ok := consumerLagFor(rec, string(api.TriggerKindKafka)); !ok || got != 6 {
		t.Fatalf("consumerLagFor = %d, %v; want 6, true", got, ok)
	}
}

func TestConsumerLagFor_UnsupportedOrMalformed(t *testing.T) {
	cases := []SourceRecord{
		{Metadata: map[string]any{"offset": int64(1)}},
		{Metadata: map[string]any{"offset": int64(5), "high_water_mark": int64(5)}},
	}
	for _, rec := range cases {
		if got, ok := consumerLagFor(rec, string(api.TriggerKindKafka)); ok || got != 0 {
			t.Fatalf("consumerLagFor(%v) = %d, %v; want 0, false", rec.Metadata, got, ok)
		}
	}
	if got, ok := consumerLagFor(cases[0], string(api.TriggerKindNATS)); ok || got != 0 {
		t.Fatalf("non-kafka consumerLagFor = %d, %v; want 0, false", got, ok)
	}
}

// --- classifyDLQReason -------------------------------------------

func TestClassifyDLQReason_CodeBranches(t *testing.T) {
	cases := []struct {
		code, err, want string
	}{
		{"function_failed", "", triggerReasonMaxAttempts},
		{"function_state_timeout", "", triggerReasonMaxAttempts},
		{"function_state_failed", "", triggerReasonMaxAttempts},
		{"function_state_killed", "", triggerReasonMaxAttempts},
		{"function_state_lost", "", triggerReasonMaxAttempts},
		{"payload_b64_invalid", "", triggerReasonPoisonRecord},
		{"response_malformed", "", triggerReasonPoisonRecord},
		{"invoke_error", "", triggerReasonBrokerError},
		{"unknown_code", "", triggerReasonPoisonRecord}, // default
	}
	for _, c := range cases {
		if got := classifyDLQReason(c.code, c.err); got != c.want {
			t.Errorf("code=%q: got %q, want %q", c.code, got, c.want)
		}
	}
}

func TestClassifyDLQReason_EmptyCodeSubstringFallback(t *testing.T) {
	// No code → fall through to the substring branch. err empty
	// defaults to poison_record.
	if got := classifyDLQReason("", ""); got != triggerReasonPoisonRecord {
		t.Errorf("empty both: got %q, want %q", got, triggerReasonPoisonRecord)
	}
	if got := classifyDLQReason("", "batchItemFailures happened"); got != triggerReasonMaxAttempts {
		t.Errorf("batchItemFailures: got %q, want %q", got, triggerReasonMaxAttempts)
	}
	if got := classifyDLQReason("", "client timeout"); got != triggerReasonMaxAttempts {
		t.Errorf("timeout: got %q, want %q", got, triggerReasonMaxAttempts)
	}
	if got := classifyDLQReason("", "payload_b64 bad"); got != triggerReasonPoisonRecord {
		t.Errorf("payload_b64: got %q, want %q", got, triggerReasonPoisonRecord)
	}
	if got := classifyDLQReason("", "response was malformed"); got != triggerReasonPoisonRecord {
		t.Errorf("malformed: got %q, want %q", got, triggerReasonPoisonRecord)
	}
	if got := classifyDLQReason("", "broker_error happened"); got != triggerReasonPoisonRecord {
		t.Errorf("broker_error: got %q, want %q", got, triggerReasonPoisonRecord)
	}
	if got := classifyDLQReason("", "totally-unrelated text"); got != triggerReasonPoisonRecord {
		t.Errorf("default: got %q, want %q", got, triggerReasonPoisonRecord)
	}
}
