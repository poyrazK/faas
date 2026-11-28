package api

import (
	"strings"
	"testing"
)

func TestRedactURLUserinfo(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"https://user:pass@example.com/x?q=1": "https://redacted@example.com/x?q=1",
		"https://token@example.com/x":         "https://redacted@example.com/x",
		"https://example.com/x":               "https://example.com/x",
		"":                                    "",
		"://bad":                              "://bad",
	} {
		if got := RedactURLUserinfo(in); got != want {
			t.Errorf("RedactURLUserinfo(%q) = %q, want %q", in, got, want)
		}
	}
	row := AppWebhookResponseFromRow(AppWebhookRow{TargetURL: "https://u:secret@example.com/h"})
	if strings.Contains(row.TargetURL, "secret") {
		t.Fatalf("response target_url leaks the password: %s", row.TargetURL)
	}
}
