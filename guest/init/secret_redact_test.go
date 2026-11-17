package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSecretRedactor_MasksValuesAcrossWrites(t *testing.T) {
	pem := "-----BEGIN KEY-----\nAAAABBBB\n-----END KEY-----"
	tests := []struct {
		name    string
		secrets map[string]string
		writes  []string
		want    string
	}{
		{
			name:    "whole line",
			secrets: map[string]string{"STRIPE_KEY": "sk_live_abc123"},
			writes:  []string{"key=sk_live_abc123 ok\n"},
			want:    "key=[REDACTED:STRIPE_KEY] ok\n",
		},
		{
			name:    "value split across writes",
			secrets: map[string]string{"STRIPE_KEY": "sk_live_abc123"},
			writes:  []string{"key=sk_li", "ve_ab", "c123\n"},
			want:    "key=[REDACTED:STRIPE_KEY]\n",
		},
		{
			name:    "multi-line value",
			secrets: map[string]string{"TLS_KEY": pem},
			writes:  []string{"loaded " + pem[:25], pem[25:] + "\n"},
			want:    "loaded [REDACTED:TLS_KEY]\n",
		},
		{
			name:    "short values stay readable",
			secrets: map[string]string{"DEBUG": "true", "PORT": "8080"},
			writes:  []string{"DEBUG=true PORT=8080\n"},
			want:    "DEBUG=true PORT=8080\n",
		},
		{
			name:    "longest value wins",
			secrets: map[string]string{"A": "password", "B": "password-2026"},
			writes:  []string{"x password-2026 password\n"},
			want:    "x [REDACTED:B] [REDACTED:A]\n",
		},
		{
			name:    "prefix that never completes is flushed",
			secrets: map[string]string{"TOKEN": "tok_secret_value"},
			writes:  []string{"partial tok_sec"},
			want:    "partial tok_sec",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			w := newSecretRedactor(tt.secrets).writer(&out)
			for _, chunk := range tt.writes {
				if n, err := w.Write([]byte(chunk)); err != nil || n != len(chunk) {
					t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
				}
			}
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
			if got := out.String(); got != tt.want {
				t.Fatalf("output = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSecretRedactor_ReloadKeepsOldValues(t *testing.T) {
	var out bytes.Buffer
	r := newSecretRedactor(map[string]string{"API_KEY": "old-value-1234"})
	w := r.writer(&out)
	r.update(map[string]string{"API_KEY": "new-value-5678"})
	_, _ = w.Write([]byte("old-value-1234 new-value-5678\n"))
	_ = w.Flush()
	if got, want := out.String(), "[REDACTED:API_KEY] [REDACTED:API_KEY]\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestSecretRedactor_HoldsBackOnlyAPossibleValue(t *testing.T) {
	var out bytes.Buffer
	w := newSecretRedactor(map[string]string{"TOKEN": "tok_secret_value"}).writer(&out)
	_, _ = w.Write([]byte("line one\nline two to"))
	if got := out.String(); got != "line one\nline two " {
		t.Fatalf("forwarded %q before the tail resolved, want everything but the possible value start", got)
	}
	_, _ = w.Write([]byte("day\n"))
	if got := out.String(); !strings.HasSuffix(got, "line two today\n") {
		t.Fatalf("output = %q, want the held tail forwarded once it diverged", got)
	}
}
