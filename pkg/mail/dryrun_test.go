package mail

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestRenderAllTemplates_HeadersApplied pins that the quota
// template carries the List-Unsubscribe pair when an unsubscribe
// URL is supplied, but the dunning / billing templates do NOT —
// the policy table pkg/mail/headers.go documents (issue #246:
// Gmail/Yahoo bulk-sender rules target *promotional* mail; a
// customer who one-click-unsubscribes from "your payment failed"
// stops receiving the suspension warning).
func TestRenderAllTemplates_HeadersApplied(t *testing.T) {
	renders, err := RenderAllTemplates("https://faas.example.test/u", time.Now())
	if err != nil {
		t.Fatalf("RenderAllTemplates: %v", err)
	}

	var quota, suspended, deletion, payment *RenderTemplate
	for i := range renders {
		switch renders[i].Name {
		case "quota_warning":
			quota = &renders[i]
		case "account_suspended":
			suspended = &renders[i]
		case "account_deletion_pending":
			deletion = &renders[i]
		case "payment_failed":
			payment = &renders[i]
		}
	}

	if quota == nil || suspended == nil || deletion == nil || payment == nil {
		t.Fatalf("missing template(s): %+v", renders)
	}

	// Quota: marketing headers applied. RFC 8058 requires the
	// URL be enclosed in angle brackets — pkg/mail/headers.go
	// does the wrapping so the policy is in one place.
	if got := quota.Headers["List-Unsubscribe"]; got != "<https://faas.example.test/u>" {
		t.Errorf("quota List-Unsubscribe = %q, want <https://faas.example.test/u>", got)
	}
	if got := quota.Headers["List-Unsubscribe-Post"]; got != "List-Unsubscribe=One-Click" {
		t.Errorf("quota List-Unsubscribe-Post = %q, want List-Unsubscribe=One-Click", got)
	}

	// Dunning / billing / deletion: MUST NOT carry one-click
	// unsubscribe. A typo here would let a customer silently
	// unsubscribe from suspension / payment-failed notices and
	// get deleted without warning.
	if len(suspended.Headers) != 0 {
		t.Errorf("account_suspended must not carry marketing headers; got %v", suspended.Headers)
	}
	if len(deletion.Headers) != 0 {
		t.Errorf("account_deletion_pending must not carry marketing headers; got %v", deletion.Headers)
	}
	if len(payment.Headers) != 0 {
		t.Errorf("payment_failed must not carry marketing headers; got %v", payment.Headers)
	}
}

// TestRenderAllTemplates_BadUnsubscribeURL pins that a malformed
// URL fails the dry-run loudly — better to surface the
// misconfiguration at the operator's terminal than ship a
// payload with a broken header.
func TestRenderAllTemplates_BadUnsubscribeURL(t *testing.T) {
	for _, bad := range []string{"mailto:unsub@x", "ftp://x", "no-scheme"} {
		if _, err := RenderAllTemplates(bad, time.Now()); err == nil {
			t.Errorf("RenderAllTemplates(%q): err = nil, want error", bad)
		}
	}
}

// TestRenderAllTemplates_StableIDs pins that the MessageID is
// derived from (account_id, template, day) and is stable across
// repeated calls within the same UTC day. The HTTP retry
// idempotency guarantee on Resend / Postmark depends on this
// being deterministic.
func TestRenderAllTemplates_StableIDs(t *testing.T) {
	day := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	r1, err := RenderAllTemplates("", day)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := RenderAllTemplates("", day)
	if err != nil {
		t.Fatal(err)
	}
	if len(r1) != len(r2) {
		t.Fatalf("len mismatch: %d vs %d", len(r1), len(r2))
	}
	for i := range r1 {
		if r1[i].MessageID != r2[i].MessageID {
			t.Errorf("%s: MessageID drift: %q vs %q", r1[i].Name, r1[i].MessageID, r2[i].MessageID)
		}
	}
}

// TestRenderAllTemplates_StableIDsAcrossDays pins that the
// MessageID changes when the day changes — the production
// quota-warning sender depends on this so a re-render after
// midnight doesn't dedupe with yesterday's send.
func TestRenderAllTemplates_StableIDsAcrossDays(t *testing.T) {
	day1 := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	r1, _ := RenderAllTemplates("", day1)
	r2, _ := RenderAllTemplates("", day2)
	if r1[0].MessageID == r2[0].MessageID {
		t.Errorf("MessageID stable across days: %q", r1[0].MessageID)
	}
}

// TestWriteDryRunJSON_Shape pins the wire format operators see.
// A future change that breaks the field names (e.g. renaming
// "subject" to "Subject") would break every operator's grep
// pattern + any tooling pinned on the shape, so the table is
// pinned here.
func TestWriteDryRunJSON_Shape(t *testing.T) {
	renders, err := RenderAllTemplates("", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteDryRunJSON(&buf, renders); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// Top-level: a JSON array.
	if !strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Errorf("output does not start with [; got: %.40s", out)
	}

	// Round-trip into the typed shape to confirm every field
	// survives the encoder.
	var roundTrip []RenderTemplate
	if err := json.Unmarshal(buf.Bytes(), &roundTrip); err != nil {
		t.Fatalf("re-parse: %v\nbody=%s", err, out)
	}
	if len(roundTrip) != len(renders) {
		t.Fatalf("round-trip len = %d, want %d", len(roundTrip), len(renders))
	}
	for _, r := range roundTrip {
		if r.Name == "" || r.Subject == "" || r.TextBody == "" || r.MessageID == "" {
			t.Errorf("missing required field on %+v", r)
		}
	}
}

// productNameFaas matches "faas" used as a word, not inside a host or path
// such as faas.example.test.
var productNameFaas = regexp.MustCompile(`(?i)(^|[^\w./-])faas([^\w./-]|$)`)

// production-us hunt #7 (H5-74): every lifecycle mail still said "Your faas
// account" and signed "— onebox faas", the internal codename. Customers know
// the product as Gregale.
func TestRenderAllTemplates_UseProductName(t *testing.T) {
	renders, err := RenderAllTemplates("https://faas.example.test/u", time.Now())
	if err != nil {
		t.Fatalf("RenderAllTemplates: %v", err)
	}
	for _, r := range renders {
		for part, text := range map[string]string{"subject": r.Subject, "text": r.TextBody, "html": r.HTMLBody} {
			if loc := productNameFaas.FindStringIndex(text); loc != nil {
				t.Errorf("%s %s names the product %q, want Gregale", r.Name, part, text[max(0, loc[0]-20):min(len(text), loc[1]+20)])
			}
		}
		if !strings.Contains(r.Subject+r.TextBody, "Gregale") {
			t.Errorf("%s never names Gregale", r.Name)
		}
	}
}
