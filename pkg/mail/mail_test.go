package mail_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/mail"
)

// TestNoopSender_ReturnsNil is the contract: the noop never errors.
func TestNoopSender_ReturnsNil(t *testing.T) {
	t.Parallel()
	if err := (mail.NoopSender{}).Send(context.Background(), mail.Message{To: []string{"x"}}); err != nil {
		t.Fatalf("noop send: %v", err)
	}
}

// TestLogSender_WritesJSON: the log-only sender emits one structured
// record per message. The record is JSON so log aggregators can index
// on (to, subject) without regex.
func TestLogSender_WritesJSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	s := mail.NewLogSender(log)
	if err := s.Send(context.Background(), mail.Message{
		To: []string{"alice@example.com"}, Subject: "hi",
		TextBody: "hello", HTMLBody: "<p>hello</p>",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	// The line is one JSON object per Send call.
	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line); err != nil {
		t.Fatalf("not JSON: %v (buf=%q)", err, buf.String())
	}
	if line["msg"] != "mail.send" {
		t.Fatalf("msg = %v, want mail.send", line["msg"])
	}
	if line["subject"] != "hi" {
		t.Fatalf("subject = %v, want hi", line["subject"])
	}
	if line["has_html"] != true {
		t.Fatalf("has_html = %v, want true", line["has_html"])
	}
}
