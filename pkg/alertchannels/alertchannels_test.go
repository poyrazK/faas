package alertchannels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestValidateSlackURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://hooks.slack.com/services/T000/B000/XXXXXXXX":       true,
		"http://hooks.slack.com/services/T000/B000/XXXX":            false,
		"https://hooks.slack.com.evil.example/services/T/B/X":       false,
		"https://evil.example/services/T/B/X":                       false,
		"https://hooks.slack.com/services/T/B/X?redirect=https://x": false,
		"https://user@hooks.slack.com/services/T/B/X":               false,
		"https://hooks.slack.com/services/T/B/../../../internal":    false,
		"https://hooks.slack.com/workflows/T/A/123/abc":             false,
		"https://169.254.169.254/latest/meta-data/services/T/B/X":   false,
	} {
		if err := ValidateSlackURL(raw); (err == nil) != ok {
			t.Errorf("ValidateSlackURL(%q) err=%v, want ok=%v", raw, err, ok)
		}
	}
}

func TestValidatePagerDuty(t *testing.T) {
	key := strings.Repeat("a", 32)
	if err := ValidatePagerDuty(key, RegionEU); err != nil {
		t.Fatal(err)
	}
	if ValidatePagerDuty("short", RegionUS) == nil || ValidatePagerDuty(key, "ap") == nil {
		t.Fatal("bad key or region accepted")
	}
}

func fire() Message {
	return Message{Event: EventFire, RuleName: "checkout errors", AppSlug: "shop", Metric: "error_rate_pct", Comparison: "gt", Threshold: 5, Observed: 12.5, Window: "5m",
		DashboardURL: "https://gregale.dev/dashboard/apps/shop", OccurredAt: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC), DedupKey: "rule-1"}
}

func TestPagerDutyFireAndResolveShareDedupKey(t *testing.T) {
	trig := PagerDutyPayload("k", fire())
	res := fire()
	res.Event = EventResolve
	resolve := PagerDutyPayload("k", res)
	if trig["event_action"] != "trigger" || resolve["event_action"] != "resolve" || trig["dedup_key"] != resolve["dedup_key"] {
		t.Fatalf("trigger %v / resolve %v", trig, resolve)
	}
	if _, ok := resolve["payload"]; ok {
		t.Fatal("a resolve carries no payload")
	}
	summary := trig["payload"].(map[string]any)["summary"].(string)
	if !strings.Contains(summary, "error_rate_pct on shop is 12.5") || !strings.Contains(summary, "threshold > 5") {
		t.Fatalf("summary = %q", summary)
	}
}

func TestSlackAndEmailContent(t *testing.T) {
	s := SlackPayload(fire())
	if !strings.Contains(s["text"].(string), "Alert: checkout errors") || len(s["blocks"].([]map[string]any)) != 2 {
		t.Fatalf("slack = %v", s)
	}
	subject, body := EmailContent(fire())
	if subject != "[Gregale] Alert: checkout errors" || !strings.Contains(body, "Dashboard: https://gregale.dev/dashboard/apps/shop") {
		t.Fatalf("email = %q / %q", subject, body)
	}
}

func TestPostJSONRetriesTransientAndStopsOnConfigErrors(t *testing.T) {
	var calls atomic.Int32
	codes := []int{503, 429, 200}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("bad request: %v", err)
		}
		w.WriteHeader(codes[min(int(n)-1, len(codes)-1)])
	}))
	defer srv.Close()
	s := &Sender{HTTP: srv.Client(), Backoff: time.Millisecond}
	if err := s.postJSON(context.Background(), srv.URL, map[string]any{"a": 1}); err != nil || calls.Load() != 3 {
		t.Fatalf("err %v after %d calls; want success on the third", err, calls.Load())
	}

	calls.Store(0)
	codes = []int{404}
	if err := s.postJSON(context.Background(), srv.URL, map[string]any{}); err == nil || calls.Load() != 1 {
		t.Fatalf("404 must be final: err %v, %d calls", err, calls.Load())
	}
}

type sentMail struct{ to, subject, body, key string }

type fakeMail struct{ got []sentMail }

func (f *fakeMail) SendEmail(_ context.Context, to, subject, body, key string) error {
	f.got = append(f.got, sentMail{to, subject, body, key})
	return nil
}

func TestSendEmailAndRejectsBadTargets(t *testing.T) {
	m := &fakeMail{}
	s := &Sender{Mail: m}
	if err := s.Send(context.Background(), Target{Kind: KindEmail, Email: "ops@example.com"}, fire()); err != nil {
		t.Fatal(err)
	}
	if len(m.got) != 1 || m.got[0].to != "ops@example.com" || m.got[0].key == "" {
		t.Fatalf("mail = %+v", m.got)
	}
	if s.Send(context.Background(), Target{Kind: KindSlack, SlackURL: "https://evil.example/services/T/B/X"}, fire()) == nil {
		t.Fatal("a non-Slack URL must never be posted to")
	}
}
