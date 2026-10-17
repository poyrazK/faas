package internalmail

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRetentionConfigurationIsDefaultOffAndPreviewOnly(t *testing.T) {
	base := map[string]string{EnableEnv: "1", "FAAS_INTERNAL_MAIL_TOKEN": testToken, "FAAS_INTERNAL_MAIL_AGE_IDENTITY_PATH": "/private/mail.age", "FAAS_INTERNAL_MAIL_SENDING_IP": "192.0.2.10"}
	get := func(key string) string { return base[key] }
	for _, value := range []string{"", "0", "true", "yes", " 1"} {
		base[RetentionEnableEnv] = value
		cfg, err := LoadConfig(func(key string) string {
			if strings.HasPrefix(key, "FAAS_INTERNAL_MAIL_RETENTION_") && key != RetentionEnableEnv {
				t.Fatal("disabled retention read policy")
			}
			return get(key)
		})
		if err != nil || cfg.Retention {
			t.Fatal("retention did not stay off", err)
		}
	}
	base[RetentionEnableEnv] = "1"
	cfg, err := LoadConfig(get)
	if err != nil || !cfg.Retention || cfg.RetentionPolicy != DefaultRetentionPolicy() || !cfg.RetentionPolicy.DryRun {
		t.Fatal("retention did not default to bounded previews", err)
	}
	for _, tc := range []struct{ key, value string }{
		{"FAAS_INTERNAL_MAIL_RETENTION_DAYS", "0"},
		{"FAAS_INTERNAL_MAIL_RETENTION_DAYS", "-1"},
		{"FAAS_INTERNAL_MAIL_RETENTION_DAYS", "366"},
		{"FAAS_INTERNAL_MAIL_RETENTION_DAYS", "7d"},
		{"FAAS_INTERNAL_MAIL_RETENTION_BATCH_SIZE", "0"},
		{"FAAS_INTERNAL_MAIL_RETENTION_BATCH_SIZE", "101"},
		{"FAAS_INTERNAL_MAIL_RETENTION_DRY_RUN", "true"},
		{"FAAS_INTERNAL_MAIL_RETENTION_DRY_RUN", " 0"},
	} {
		base[tc.key] = tc.value
		if _, err := LoadConfig(get); err == nil {
			t.Fatal("invalid retention policy accepted", tc.key)
		}
		delete(base, tc.key)
	}
	base["FAAS_INTERNAL_MAIL_RETENTION_DRY_RUN"] = "0"
	if cfg, err := LoadConfig(get); err != nil || cfg.RetentionPolicy.DryRun {
		t.Fatal("explicit application mode rejected", err)
	}
}

func TestPayloadFingerprintIsStableAndBindsIdentityNamespaceAndResource(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("private short verification code 123456")
	codec := AgeCodec{Identity: identity, Namespace: "platform"}
	expected, err := codec.Fingerprint("message:one", plain)
	if err != nil || len(expected) != 32 || bytes.Contains(expected, plain) {
		t.Fatal("invalid private verifier", err)
	}
	restarted := AgeCodec{Identity: identity, Namespace: "platform"}
	actual, err := restarted.Fingerprint("message:one", plain)
	if err != nil || !bytes.Equal(expected, actual) {
		t.Fatal("restart changed idempotency verifier", err)
	}
	for _, tc := range []struct {
		codec    AgeCodec
		resource string
		plain    []byte
	}{
		{codec, "message:two", plain},
		{AgeCodec{Identity: identity, Namespace: "other"}, "message:one", plain},
		{AgeCodec{Identity: other, Namespace: "platform"}, "message:one", plain},
		{codec, "message:one", []byte("another short code")},
	} {
		actual, err := tc.codec.Fingerprint(tc.resource, tc.plain)
		if err != nil || bytes.Equal(expected, actual) {
			t.Fatal("verifier escaped its content/resource/key boundary", err)
		}
	}
}

func TestRetentionDefaultOffAndCredentialScopeReadNoInputOrStore(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	s := &Service{Enabled: func() bool { return true }, RetentionEnabled: enabled.Load, RetentionPolicy: DefaultRetentionPolicy(), ReportsEnabled: func() bool { return true }, ReportToken: reportToken}
	handler, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		enabled bool
		token   string
		want    int
	}{
		{false, testToken, 404},
		{true, reportToken, 401},
		{true, feedbackToken, 401},
		{true, "customer-token", 401},
	} {
		enabled.Store(tc.enabled)
		r := httptest.NewRequest(http.MethodPost, RetentionPath, forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+tc.token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal("retention permission boundary", w.Code, tc.want)
		}
	}
	enabled.Store(false)
	if _, err := s.RetainPayloads(t.Context(), false); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled cleanup accessed store", err)
	}
	enabled.Store(true)
	r := httptest.NewRequest(http.MethodPost, RetentionPath, strings.NewReader(`{"dry_run":false}`))
	r.Header.Set("Authorization", "Bearer "+testToken)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "retention_dry_run_required") {
		t.Fatal("HTTP overrode preview-only configuration", w.Code)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s.RunRetention(ctx, func(error) { t.Fatal("cancelled retention worker did work") })
	if DefaultRetentionPolicy().BatchSize > api.InternalMailMaxRetentionBatchSize {
		t.Fatal("invalid bounded default")
	}
}
