package internalmail

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestEncryptionGateAndCredentialBoundaries(t *testing.T) {
	for _, flag := range []string{"", "0", "true", " 1", "1 "} {
		if EncryptionRotationEnabled(func(key string) string {
			if key == EnableEnv {
				return "1"
			}
			return flag
		}) {
			t.Fatal("encryption flag accepted a nonexact value")
		}
	}
	if EncryptionRotationEnabled(func(key string) string {
		if key != EnableEnv {
			t.Fatal("disabled master read encryption configuration")
		}
		return "0"
	}) {
		t.Fatal("disabled master enabled rotation")
	}
	var enabled atomic.Bool
	s := &Service{Enabled: func() bool { return true }, OperationsEnabled: func() bool { return true }, OperationsToken: operatorToken, EncryptionRotationEnabled: enabled.Load}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{EncryptionPath, EncryptionPath + "/preview", EncryptionPath + "/activate", EncryptionPath + "/rewrap", EncryptionPath + "/retire"} {
		r := httptest.NewRequest(http.MethodPost, path, forbiddenRead{t})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("disabled encryption route visible", path, w.Code)
		}
	}
	if _, err := s.ChangeEncryption(t.Context(), "activate", EncryptionRequest{}); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled service accessed key/store state", err)
	}
	enabled.Store(true)
	for _, token := range []string{testToken, reportToken, feedbackToken, producerToken, ""} {
		r := httptest.NewRequest(http.MethodPost, EncryptionPath+"/rewrap", forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("non-operator encryption access", w.Code)
		}
	}
}

func TestEncryptionRequestBounds(t *testing.T) {
	revision := int64(0)
	valid := EncryptionRequest{OperatorAuditInput: auditInput(), KeyID: strings.Repeat("a", 64), ExpectedRevision: &revision}
	if valid.Validate("activate") != nil || valid.Validate("retire") != nil || valid.Validate("rewrap") == nil {
		t.Fatal("invalid mutation shape accepted")
	}
	for _, mutate := range []func(*EncryptionRequest){
		func(r *EncryptionRequest) { r.ExpectedRevision = nil },
		func(r *EncryptionRequest) { v := int64(-1); r.ExpectedRevision = &v },
		func(r *EncryptionRequest) { r.KeyID = strings.Repeat("A", 64) },
		func(r *EncryptionRequest) { r.KeyID = "raw-secret" },
		func(r *EncryptionRequest) { r.BatchSize = 1 },
		func(r *EncryptionRequest) { r.Evidence = "mail body with spaces" },
	} {
		input := valid
		mutate(&input)
		if input.Validate("activate") == nil {
			t.Fatal("invalid activation accepted")
		}
	}
	for _, size := range []int{-1, 0, 26, 100000} {
		input := valid
		input.BatchSize = size
		if input.Validate("rewrap") == nil {
			t.Fatal("unbounded migration accepted")
		}
	}
	valid.BatchSize = 10
	if valid.Validate("rewrap") != nil || valid.Validate("unknown") == nil {
		t.Fatal("valid bounded migration rejected")
	}
}

type encryptionStatusFunc func(context.Context, *KeyringCodec) (EncryptionStatus, error)

func (f encryptionStatusFunc) encryptionStatus(ctx context.Context, codec *KeyringCodec) (EncryptionStatus, error) {
	return f(ctx, codec)
}

func TestEncryptionMetricsAreDefaultOffAggregateAndFailClosed(t *testing.T) {
	var enabled, failed atomic.Bool
	calls := 0
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewEncryptionMonitor(encryptionStatusFunc(func(context.Context, *KeyringCodec) (EncryptionStatus, error) {
		calls++
		if failed.Load() {
			return EncryptionStatus{}, errors.New("private database diagnostic")
		}
		return EncryptionStatus{Keys: []EncryptionKeyStatus{
			{KeyID: strings.Repeat("a", 64), Active: true, Configured: true, Payloads: 8},
			{KeyID: strings.Repeat("b", 64), Payloads: 2, DomainKeys: 1, DKIMKeys: 3, Fingerprints: 5, PrunedFingerprints: 4},
		}}, nil
	}), nil, enabled.Load))
	if text := scrapeMonitor(t, registry); calls != 0 || !strings.Contains(text, "maild_mail_encryption_enabled 0") || strings.Contains(text, "old_payloads") {
		t.Fatal("disabled encryption metrics read state")
	}
	enabled.Store(true)
	text := scrapeMonitor(t, registry)
	for _, expected := range []string{"stats_up 1", "missing_required_keys 1", "old_payloads 2", "old_signing_keys 4", "old_fingerprints 5", "pruned_old_fingerprints 4"} {
		if !strings.Contains(text, "maild_mail_encryption_"+expected) {
			t.Fatal("missing encryption aggregate", expected)
		}
	}
	metrics, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range metrics {
		if len(family.Metric) != 1 || len(family.Metric[0].Label) != 0 {
			t.Fatal("key identifier appeared in metric labels")
		}
	}
	failed.Store(true)
	text = scrapeMonitor(t, registry)
	if !strings.Contains(text, "maild_mail_encryption_stats_up 0") || strings.Contains(text, "old_payloads") || strings.Contains(text, "private") {
		t.Fatal("failed inventory looked empty or exposed diagnostics")
	}
}
