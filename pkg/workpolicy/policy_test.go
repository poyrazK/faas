package workpolicy

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPolicyValidationAndDeadlines(t *testing.T) {
	base := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	p := Policy{Name: "index-document", MaxRunningPerKey: 1, PendingUpdates: PendingKeepLatest,
		Debounce: 3 * time.Second, ExpiresAfter: 10 * time.Minute}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.MaxRunningPerFairnessKey = 2
	if err := p.Validate(); err != nil {
		t.Fatalf("valid fairness cap: %v", err)
	}
	if got := p.AvailableAt(base, base); !got.Equal(base.Add(3 * time.Second)) {
		t.Fatalf("debounced availability = %s", got)
	}
	if got := p.AvailableAt(base, base.Add(time.Minute)); !got.Equal(base.Add(time.Minute)) {
		t.Fatalf("delayed availability = %s", got)
	}
	if got := p.ExpiresAt(base); got == nil || !got.Equal(base.Add(10*time.Minute)) {
		t.Fatalf("expiry = %v", got)
	}
	for _, mutate := range []func(*Policy){
		func(v *Policy) { v.MaxRunningPerKey = 2 },
		func(v *Policy) { v.ExpiresAfter = v.Debounce },
		func(v *Policy) { v.MaxRunningPerFairnessKey = 1001 },
		func(v *Policy) { v.PendingUpdates = "replace_running" },
	} {
		invalid := p
		mutate(&invalid)
		if err := invalid.Validate(); err == nil {
			t.Fatalf("accepted unsupported policy: %+v", invalid)
		}
	}
}

func TestSelectorResolvesDistinctCanonicalKeys(t *testing.T) {
	selector, err := ParseSelector("data.document_id")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ payload, want string }{
		{`{"data":{"document_id":"1"}}`, "s:1"},
		{`{"data":{"document_id":1}}`, "n:1"},
		{`{"data":{"document_id":1.0}}`, "n:1"},
		{`{"data":{"document_id":true}}`, "b:true"},
	} {
		got, err := selector.Resolve(json.RawMessage(tc.payload))
		if err != nil || got != tc.want {
			t.Fatalf("Resolve(%s) = %q, %v; want %q", tc.payload, got, err, tc.want)
		}
	}
	for _, payload := range []string{
		`{"data":{}}`, `{"data":{"document_id":null}}`,
		`{"data":{"document_id":[]}}`, `{"data":{"document_id":{}}}`,
		`{"data":{"document_id":1}} trailing`,
		`{"data":{"document_id":1}} {"data":{"document_id":2}}`,
		`{"data":{"document_id":1e1000000}}`,
	} {
		if _, err := selector.Resolve(json.RawMessage(payload)); err == nil {
			t.Fatalf("accepted invalid work key in %s", payload)
		}
	}
}

func TestDigestKeyRejectsNoncanonicalInput(t *testing.T) {
	for _, key := range []string{"", "s:", "n:01", "n:garbage", "b:TRUE", "s:ok" + string(make([]byte, MaxKeyBytes))} {
		if _, err := DigestKey(key); err == nil {
			t.Fatalf("accepted key %q", key)
		}
	}
	stringDigest, err := DigestKey("s:1")
	if err != nil {
		t.Fatal(err)
	}
	numericDigest, err := DigestKey("n:1")
	if err != nil || stringDigest == numericDigest {
		t.Fatalf("typed keys collided or failed: %v", err)
	}
}

func TestSelectorRejectsAmbiguousPaths(t *testing.T) {
	for _, path := range []string{"", "data..id", "data.*", "data[0]", "data.id.more.a.b.c.d.e.f"} {
		if _, err := ParseSelector(path); err == nil {
			t.Fatalf("accepted selector %q", path)
		}
	}
}
