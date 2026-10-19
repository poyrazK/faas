// poller_decoders_pure_test.go — fill pkg/sched/poller_queue.go +
// poller_nats.go coverage of the tiny pure JSON helpers. Targets
// parseJSONHeaders, parseJSONMetadata, and jsonStdUnmarshal — all
// currently 0%-covered.
//
// Whitebox `package sched`.

package sched_test

import (
	"github.com/onebox-faas/faas/pkg/sched"
	"testing"
)

// --- parseJSONHeaders --------------------------------------------

func TestParseJSONHeaders_EmptyReturnsNil(t *testing.T) {
	if got := sched.ParseJSONHeaders(""); got != nil {
		t.Errorf("empty: %v, want nil", got)
	}
}

func TestParseJSONHeaders_ValidJSON(t *testing.T) {
	got := sched.ParseJSONHeaders(`{"X-Foo":"bar","X-Baz":"qux"}`)
	if got == nil || got["X-Foo"] != "bar" || got["X-Baz"] != "qux" {
		t.Errorf("got %v", got)
	}
}

func TestParseJSONHeaders_InvalidReturnsNil(t *testing.T) {
	// Per doc: malformed JSON is swallowed (one record's problem,
	// not the batch's). Pin the swallow.
	if got := sched.ParseJSONHeaders("not json"); got != nil {
		t.Errorf("invalid: %v, want nil", got)
	}
}

func TestParseJSONHeaders_EmptyObjectReturnsEmptyMap(t *testing.T) {
	got := sched.ParseJSONHeaders("{}")
	if got == nil {
		t.Fatal("empty object: nil, want empty (non-nil) map")
	}
	if len(got) != 0 {
		t.Errorf("empty object: got %v", got)
	}
}

// --- parseJSONMetadata -------------------------------------------

func TestParseJSONMetadata_EmptyReturnsNil(t *testing.T) {
	if got := sched.ParseJSONMetadata(""); got != nil {
		t.Errorf("empty: %v, want nil", got)
	}
}

func TestParseJSONMetadata_ValidJSON(t *testing.T) {
	got := sched.ParseJSONMetadata(`{"partition":7,"stream":"events"}`)
	if got == nil {
		t.Fatal("nil result")
	}
	if got["partition"] != float64(7) { // JSON numbers decode as float64
		t.Errorf("partition = %v, want 7", got["partition"])
	}
	if got["stream"] != "events" {
		t.Errorf("stream = %v", got["stream"])
	}
}

func TestParseJSONMetadata_InvalidReturnsNil(t *testing.T) {
	if got := sched.ParseJSONMetadata("not json"); got != nil {
		t.Errorf("invalid: %v, want nil", got)
	}
}

func TestParseJSONMetadata_EmptyObjectReturnsEmptyMap(t *testing.T) {
	got := sched.ParseJSONMetadata("{}")
	if got == nil {
		t.Fatal("empty object: nil, want empty (non-nil) map")
	}
	if len(got) != 0 {
		t.Errorf("empty object: got %v", got)
	}
}

// --- jsonStdUnmarshal --------------------------------------------

func TestJsonStdUnmarshal_Valid(t *testing.T) {
	var got map[string]int
	if err := sched.JsonStdUnmarshal([]byte(`{"x":1}`), &got); err != nil {
		t.Fatalf("err = %v", err)
	}
	if got["x"] != 1 {
		t.Errorf("got %v", got)
	}
}

func TestJsonStdUnmarshal_Invalid(t *testing.T) {
	var got map[string]int
	if err := sched.JsonStdUnmarshal([]byte("not json"), &got); err == nil {
		t.Error("invalid input: err = nil, want error")
	}
}
