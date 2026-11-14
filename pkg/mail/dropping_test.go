package mail_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/mail"
)

type countingMetrics struct {
	mu       sync.Mutex
	failures map[string]int
}

func (c *countingMetrics) RecordFailure(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failures == nil {
		c.failures = map[string]int{}
	}
	c.failures[reason]++
}

func (c *countingMetrics) RecordRetry(string) {}

// Production ran FAAS_MAIL_TRANSPORT=noop with no provider: signup minted its
// verification token, Send "succeeded", and nothing said the link never left
// the box. On a non-dev box the transport must say so at boot and on every
// drop, and count the drop, while still returning nil so callers keep their
// behaviour.
func TestNoopOnProductionBoxDropsLoudly(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	getenv := func(k string) string {
		if k == "FAAS_MAIL_TRANSPORT" {
			return "noop"
		}
		return ""
	}
	s, err := mail.SenderFromEnv(getenv, log)
	if err != nil {
		t.Fatalf("SenderFromEnv: %v", err)
	}
	boot := buf.String()
	if !strings.Contains(boot, `"level":"ERROR"`) || !strings.Contains(boot, `"msg":"mail.transport"`) || !strings.Contains(boot, `"delivers_mail":false`) {
		t.Fatalf("boot did not announce that no mail is delivered: %s", boot)
	}
	dropping, ok := s.(*mail.DroppingSender)
	if !ok {
		t.Fatalf("transport = %T, want *mail.DroppingSender", s)
	}
	metrics := &countingMetrics{}
	dropping.SetMetrics(metrics)
	buf.Reset()

	err = s.Send(context.Background(), mail.Message{
		To:       []string{"person@example.test"},
		Subject:  "Verify your email",
		TextBody: "https://api.example.test/v1/auth/verify-email?token=SECRETTOKEN",
	})
	if err != nil {
		t.Fatalf("Send = %v, want nil so callers keep their behaviour", err)
	}
	if got := metrics.failures[mail.ReasonNoTransport]; got != 1 {
		t.Errorf("no_transport failures = %d, want 1", got)
	}
	logged := buf.String()
	if !strings.Contains(logged, `"level":"ERROR"`) || !strings.Contains(logged, `"msg":"mail.dropped"`) || !strings.Contains(logged, "Verify your email") {
		t.Errorf("drop was not logged at ERROR with its subject: %s", logged)
	}
	for _, leak := range []string{"person@example.test", "SECRETTOKEN"} {
		if strings.Contains(logged, leak) {
			t.Errorf("drop log leaked %q: %s", leak, logged)
		}
	}
}

func TestNoopOnDevBoxStaysSilent(t *testing.T) {
	var buf bytes.Buffer
	getenv := func(k string) string {
		switch k {
		case "FAAS_MAIL_TRANSPORT":
			return "noop"
		case "FAAS_DEV":
			return "1"
		}
		return ""
	}
	s, err := mail.SenderFromEnv(getenv, slog.New(slog.NewJSONHandler(&buf, nil)))
	if err != nil {
		t.Fatalf("SenderFromEnv: %v", err)
	}
	if err := s.Send(context.Background(), mail.Message{To: []string{"a@example.test"}, Subject: "x"}); err != nil {
		t.Fatalf("Send = %v", err)
	}
	if strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Errorf("a FAAS_DEV box logged an error for noop: %s", buf.String())
	}
}

func TestDroppingSenderNilMetricsAndLogAreSafe(t *testing.T) {
	d := mail.NewDroppingSender(nil)
	d.SetMetrics(nil)
	if err := d.Send(context.Background(), mail.Message{Subject: "x"}); err != nil {
		t.Fatalf("Send = %v", err)
	}
}
