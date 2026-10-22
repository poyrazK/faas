package airedact

// adr: 997 — detectors for content guardrails and capture redaction.

import (
	"strings"
	"testing"
)

func TestFind(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		kind       Kind
		want       []string
	}{
		{"email", "mail ana.b+x@mail.example.co.uk today", Email, []string{"ana.b+x@mail.example.co.uk"}},
		{"not an email", "user@localhost and @handle", Email, nil},
		{"card", "card 4111 1111 1111 1111 exp", PaymentCard, []string{"4111 1111 1111 1111"}},
		{"card dashes", "4111-1111-1111-1111", PaymentCard, []string{"4111-1111-1111-1111"}},
		{"adjacent cards", "4111111111111111,5500005555555559", PaymentCard, []string{"4111111111111111", "5500005555555559"}},
		{"card then digits", "4111 1111 1111 1111 22", PaymentCard, []string{"4111 1111 1111 1111"}},
		{"luhn fails", "order 4111111111111112", PaymentCard, nil},
		{"inside a word", "id x4111111111111111", PaymentCard, nil},
		{"short number", "call 555 0100", PaymentCard, nil},
		{"iban", "pay to DE89 3704 0044 0532 0130 00 BIC COBADEFFXXX", IBAN, []string{"DE89 3704 0044 0532 0130 00"}},
		{"iban compact", "GB82WEST12345698765432.", IBAN, []string{"GB82WEST12345698765432"}},
		{"iban checksum", "DE00 3704 0044 0532 0130 00", IBAN, nil},
		{"openai key", `key "sk-proj-AbCdEf0123456789AbCdEf0123"`, Secret, []string{"sk-proj-AbCdEf0123456789AbCdEf0123"}},
		{"anthropic key", "sk-ant-api03-abcdefghijklmnopqrstuvwxyz", Secret, []string{"sk-ant-api03-abcdefghijklmnopqrstuvwxyz"}},
		{"aws key", "AKIAIOSFODNN7EXAMPLE", Secret, []string{"AKIAIOSFODNN7EXAMPLE"}},
		{"github token", "ghp_" + strings.Repeat("a", 36), Secret, []string{"ghp_" + strings.Repeat("a", 36)}},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", Secret,
			[]string{"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"}},
		{"pem", "x -----BEGIN RSA PRIVATE KEY-----\\nMIIE\\n-----END RSA PRIVATE KEY----- y", Secret,
			[]string{"-----BEGIN RSA PRIVATE KEY-----\\nMIIE\\n-----END RSA PRIVATE KEY-----"}},
		{"plain prose", "The sk-learn library and AKIA are words", Secret, nil},
	} {
		var got []string
		for _, finding := range Find([]byte(tc.text), tc.kind) {
			got = append(got, tc.text[finding.Start:finding.End])
		}
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%s: Find = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRedact(t *testing.T) {
	text := []byte("write to a@example.com, card 4111 1111 1111 1111, key AKIAIOSFODNN7EXAMPLE")
	out, counts := Redact(text, Kinds...)
	want := "write to [REDACTED:email], card [REDACTED:payment_card], key [REDACTED:secret]"
	if string(out) != want || counts[Email] != 1 || counts[PaymentCard] != 1 || counts[Secret] != 1 {
		t.Fatalf("Redact = %q, %v", out, counts)
	}
	if out, counts := Redact([]byte("a@example.com"), PaymentCard); string(out) != "a@example.com" || counts != nil {
		t.Fatalf("a kind not asked for was redacted: %q %v", out, counts)
	}
	// A secret wins over another kind starting at the same byte.
	overlap := []byte("sk-abcdefghijklmnopqrstuvwxyz@example.com")
	if out, _ := Redact(overlap, Kinds...); string(out) != "[REDACTED:secret]@example.com" {
		t.Fatalf("overlap = %q", out)
	}
}
