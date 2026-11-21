package preview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestSharedEventOutboxContract(t *testing.T) {
	body, err := os.ReadFile("testdata/event_outbox.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Body         string  `json:"body"`
		Digest       string  `json:"digest"`
		PolicyDigest string  `json:"policy_digest"`
		TokenHex     string  `json:"token_hex"`
		SchemaDigest string  `json:"schema_digest"`
		Jitter       []int64 `json:"jitter"`
	}
	if err = json.Unmarshal(body, &f); err != nil {
		t.Fatal(err)
	}
	schemaHash := sha256.Sum256([]byte(EventOutboxSchema + "\n" + EventOutboxPayloadSchema))
	if hex.EncodeToString(schemaHash[:]) != f.SchemaDigest {
		t.Fatal("schema differs")
	}
	var request AppPublishEventRequest
	if err = decodeEventOutboxRequest(f.Body, &request); err != nil {
		t.Fatal(err)
	}
	if request.Environment != "staging" {
		t.Fatal("environment lost")
	}
	encoded, err := json.Marshal(request)
	if err != nil || string(encoded) != f.Body {
		t.Fatalf("request bytes differ: %s %v", encoded, err)
	}
	digest := sha256.Sum256(encoded)
	if hex.EncodeToString(digest[:]) != f.Digest {
		t.Fatal("digest differs")
	}
	p := EventOutboxRetryPolicy{InitialBackoff: 5 * time.Millisecond, MaxBackoff: 300 * time.Millisecond, MaxAttempts: 3}
	digest = eventOutboxPolicyDigest(p)
	if hex.EncodeToString(digest[:]) != f.PolicyDigest {
		t.Fatal("policy differs")
	}
	token, _ := hex.DecodeString(f.TokenHex)
	var c eventOutboxClaim
	copy(c.token[:], token)
	for i, n := range []int64{1, 2, 3, 9, 2147483647} {
		c.attempts = n
		if got := c.retryDelay(EventOutboxRecoveryOptions{RetryPolicy: &p}).Milliseconds(); got != f.Jitter[i] {
			t.Fatalf("jitter %d: %d", n, got)
		}
	}
	for _, bad := range []string{f.Body[:len(f.Body)-1] + `,"future_environment":"preview"}`, f.Body + ` {}`} {
		if decodeEventOutboxRequest(bad, &request) == nil {
			t.Fatal("unknown fields or trailing JSON silently dropped")
		}
	}
}

func TestEventOutboxEnvironmentValidation(t *testing.T) {
	for _, environment := range []string{" staging", "staging\n", "\x00", "this-environment-name-is-longer-than-forty-characters"} {
		if (AppPublishEventRequest{Key: "one", Data: json.RawMessage(`null`), Environment: environment}).Validate() == nil {
			t.Fatal("invalid environment accepted")
		}
	}
	if (AppPublishEventRequest{Key: "one", Data: json.RawMessage(`null`)}).Validate() != nil {
		t.Fatal("legacy request changed")
	}
}
