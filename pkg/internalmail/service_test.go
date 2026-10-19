package internalmail

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
)

const testToken = "internal-mail-test-token-32-bytes-minimum"

type testResolver map[string][]string

func (r testResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	v, ok := r[name]
	if !ok {
		return nil, errors.New("no record")
	}
	return v, nil
}

func TestFlagsAndPrivateAuthentication(t *testing.T) {
	var enabled atomic.Bool
	s := &Service{Enabled: enabled.Load}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{APIPath + "/domains", APIPath + "/messages", APIPath + "/reports", "/healthz", "/readyz"} {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer "+testToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("disabled %s: %d", path, w.Code)
		}
	}
	enabled.Store(true)
	for _, authorization := range []string{"", "Bearer customer-api-key", "Bearer " + testToken + "extra"} {
		r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		r.Header.Set("Authorization", authorization)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("bad service credential: %d", w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	enabled.Store(false)
	if _, err := s.Submit(t.Context(), "id", SendRequest{}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := s.RegisterDomain(t.Context(), "example.test"); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if err := s.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestConfigFailsClosed(t *testing.T) {
	base := map[string]string{EnableEnv: "1", "FAAS_INTERNAL_MAIL_TOKEN": testToken, "FAAS_INTERNAL_MAIL_AGE_IDENTITY_PATH": "/private/mail.age", "FAAS_INTERNAL_MAIL_SENDING_IP": "192.0.2.10"}
	for _, value := range []string{"", "0", "true", "yes", " 1"} {
		cfg, err := LoadConfig(func(key string) string {
			if key == EnableEnv {
				return value
			}
			t.Fatalf("disabled config read %s", key)
			return ""
		})
		if err != nil || cfg.Token != "" {
			t.Fatal(cfg, err)
		}
	}
	for _, tc := range []struct{ key, value string }{{"FAAS_INTERNAL_MAIL_TOKEN", "short"}, {"FAAS_INTERNAL_MAIL_SENDING_IP", "127.0.0.1"}, {"FAAS_INTERNAL_MAIL_SOCKET", "relative.sock"}, {"FAAS_INTERNAL_MAIL_NAMESPACE", "../customer"}, {"FAAS_INTERNAL_MAIL_AGE_IDENTITY_PATH", ""}} {
		t.Run(tc.key, func(t *testing.T) {
			get := func(key string) string {
				if key == tc.key {
					return tc.value
				}
				return base[key]
			}
			if _, err := LoadConfig(get); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
	get := func(key string) string { return base[key] }
	cfg, err := LoadConfig(get)
	if err != nil || cfg.Delivery {
		t.Fatal(cfg, err)
	}
	base[DeliveryEnableEnv] = "1"
	base["FAAS_INTERNAL_MAIL_ALLOWED_RECIPIENTS"] = "one@example.net,two@example.net"
	base["FAAS_INTERNAL_MAIL_SMTP_ADDR"] = "smtp.external.test:25"
	if _, err := LoadConfig(get); err == nil {
		t.Fatal("allowed remote relay")
	}
	base["FAAS_INTERNAL_MAIL_SMTP_ADDR"] = "127.0.0.1:2525"
	if _, err := LoadConfig(get); err != nil {
		t.Fatal(err)
	}
	base[ReportsEnableEnv] = "1"
	for _, token := range []string{"", "short", testToken} {
		base["FAAS_INTERNAL_MAIL_REPORT_TOKEN"] = token
		if _, err := LoadConfig(get); err == nil {
			t.Fatal("reports accepted a missing, short, or reused credential")
		}
	}
	base["FAAS_INTERNAL_MAIL_REPORT_TOKEN"] = reportToken
	if cfg, err := LoadConfig(get); err != nil || !cfg.Reports {
		t.Fatal("valid report configuration rejected", err)
	}
}

func TestCiphertextIsResourceAndNamespaceBound(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	codec := AgeCodec{Identity: identity, Namespace: "platform"}
	sealed, err := codec.Seal("message:one", []byte("secret reset token"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("secret reset token")) {
		t.Fatal("plaintext at rest")
	}
	plain, err := codec.Open("message:one", sealed)
	if err != nil || string(plain) != "secret reset token" {
		t.Fatal(err)
	}
	if _, err := codec.Open("message:other", sealed); err == nil {
		t.Fatal("accepted ciphertext from another row")
	}
	codec.Namespace = "customer"
	if _, err := codec.Open("message:one", sealed); err == nil {
		t.Fatal("accepted another namespace")
	}
}

func TestRequestValidationAndStrictJSON(t *testing.T) {
	valid := SendRequest{From: "Gregale <notify@example.test>", To: []string{"person@example.net"}, Subject: "Verification", Text: "Code: 123456"}
	for _, tc := range []struct {
		name   string
		change func(*SendRequest)
	}{
		{"header-injection", func(r *SendRequest) { r.Subject = "Hello\r\nBcc: secret@example.net" }},
		{"spoofed-from-header", func(r *SendRequest) { r.Headers = map[string]string{"from": "other@example.net"} }},
		{"duplicate-header-case", func(r *SendRequest) {
			r.Headers = map[string]string{"Reply-To": "a@example.net", "reply-to": "b@example.net"}
		}},
		{"bad-recipient", func(r *SendRequest) { r.To = []string{"person@127.0.0.1"} }},
		{"oversized-body", func(r *SendRequest) { r.Text = strings.Repeat("x", api.InternalMailMaxBodyBytes+1) }},
		{"too-many-recipients", func(r *SendRequest) { r.To = make([]string, api.InternalMailMaxRecipients+1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := valid
			tc.change(&r)
			if _, _, err := normalizeRequest(r); !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
		})
	}
	s := &Service{Enabled: func() bool { return true }}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"name":"example.test","unexpected":true}`, `{"name":"example.test"} {}`, `{"name":"` + strings.Repeat("x", api.InternalMailMaxRequestBytes) + `"}`} {
		r := httptest.NewRequest(http.MethodPost, APIPath+"/domains", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+testToken)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest && w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("strict JSON: %d", w.Code)
		}
	}
}

func TestSPFAndDomainValidation(t *testing.T) {
	ip := netip.MustParseAddr("192.0.2.10")
	for _, values := range [][]string{{"v=spf1 ip4:192.0.2.10 -all", "v=spf1 -all"}, {"v=spf1 ip4:192.0.2.11 -all"}, {"v=spf1 -all ip4:192.0.2.10"}, {"unrelated TXT"}} {
		if validSPF(values, ip) {
			t.Fatal(values)
		}
	}
	if !validSPF([]string{"unrelated TXT", "v=spf1 ip4:192.0.2.10 -all"}, ip) {
		t.Fatal("valid SPF refused")
	}
	for _, domain := range []string{"localhost", "127.0.0.1", "mail..example.com", "-mail.example.com", "example.com\r\n", "éxample.com"} {
		if validDomain(domain) {
			t.Fatal(domain)
		}
	}
}
