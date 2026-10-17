package internalmail

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

func TestRecoveryExactGateExternalIDAndDisabledInputs(t *testing.T) {
	for _, flag := range []string{"", "0", "true", "1 "} {
		if RecoveryEnabled(func(key string) string {
			if key == EnableEnv {
				return "1"
			}
			return flag
		}) {
			t.Fatal("nonexact recovery flag enabled")
		}
	}
	if RecoveryEnabled(func(key string) string {
		if key != EnableEnv {
			t.Fatal("disabled master read recovery config")
		}
		return "0"
	}) {
		t.Fatal("disabled master enabled recovery")
	}
	base := map[string]string{EnableEnv: "1", "FAAS_INTERNAL_MAIL_TOKEN": testToken, "FAAS_INTERNAL_MAIL_AGE_IDENTITY_PATH": "/private/mail.age", "FAAS_INTERNAL_MAIL_SENDING_IP": "192.0.2.10"}
	if _, err := LoadConfig(func(key string) string {
		if key == RecoveryIDEnv {
			t.Fatal("disabled recovery read external ID")
		}
		return base[key]
	}); err != nil {
		t.Fatal(err)
	}
	base[RecoveryEnableEnv] = "1"
	for _, id := range []string{"", uuid.Nil.String(), "not-a-uuid", " " + uuid.NewString()} {
		base[RecoveryIDEnv] = id
		if _, err := LoadConfig(func(key string) string { return base[key] }); err == nil {
			t.Fatal("missing or invalid external restore ID accepted")
		}
	}
	base[RecoveryIDEnv] = uuid.NewString()
	if cfg, err := LoadConfig(func(key string) string { return base[key] }); err != nil || cfg.RecoveryID != base[RecoveryIDEnv] {
		t.Fatal("valid external ID rejected", err)
	}
}

func TestRecoveryPrivateGateAndAuthBeforeBody(t *testing.T) {
	var enabled atomic.Bool
	s := &Service{Enabled: func() bool { return true }, OperationsEnabled: func() bool { return true }, OperationsToken: operatorToken, RecoveryEnabled: enabled.Load}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{RecoveryPath, RecoveryPath + "/preview", RecoveryPath + "/release", RecoveryPath + "/recipients/" + uuid.NewString()} {
		r := httptest.NewRequest(http.MethodPost, path, forbiddenRead{t})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("disabled recovery route visible", path, w.Code)
		}
	}
	if _, err := s.ReleaseRecovery(t.Context(), RecoveryReleaseRequest{}); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled service touched recovery", err)
	}
	enabled.Store(true)
	for _, token := range []string{testToken, reportToken, feedbackToken, producerToken, ""} {
		r := httptest.NewRequest(http.MethodPost, RecoveryPath+"/release", forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("nonoperator recovery access", w.Code)
		}
	}
	if err := s.DeliverOne(t.Context()); !errors.Is(err, ErrInvalid) {
		t.Fatal("enabled recovery accepted an unsupported safety store", err)
	}
	if _, err := s.Submit(t.Context(), "test", request()); !errors.Is(err, ErrInvalid) {
		t.Fatal("unfenced admission accepted", err)
	}
}

func TestRecoveryRequiresEvidenceAndAllReleaseReviews(t *testing.T) {
	status := RecoveryStatus{RecoveryID: uuid.NewString(), Revision: 1, Held: true}
	for _, outcome := range []string{"retry", "submitted"} {
		input := recoveryReview(status, "queued", outcome)
		if input.Validate() != nil {
			t.Fatal("valid review rejected")
		}
		input.ConfirmedNotAccepted, input.ConfirmedRelayAccepted = false, false
		if input.Validate() == nil {
			t.Fatal("blind recovery review accepted")
		}
	}
	for _, mutate := range []func(*RecoveryReleaseRequest){
		func(r *RecoveryReleaseRequest) { r.ConfirmedPolicy = false },
		func(r *RecoveryReleaseRequest) { r.ConfirmedAccess = false },
		func(r *RecoveryReleaseRequest) { r.ConfirmedConsumers = false },
		func(r *RecoveryReleaseRequest) { r.ConfirmedRelay = false },
		func(r *RecoveryReleaseRequest) { r.ConfirmedQuarantine = false },
		func(r *RecoveryReleaseRequest) { r.ExpectedRevision = nil },
		func(r *RecoveryReleaseRequest) { r.RecoveryID = uuid.Nil.String() },
	} {
		input := recoveryRelease(status)
		mutate(&input)
		if input.Validate() == nil {
			t.Fatal("incomplete recovery release accepted")
		}
	}
	for _, query := range []string{"limit=0", "limit=101", "limit=1&limit=2", "after=no", "after=%zz", "address=one%40example.net", "status=queued"} {
		if _, err := recoveryList(query); err == nil {
			t.Fatal("unsafe preview query accepted", query)
		}
	}
}

type recoveryMetricSource struct {
	calls int
	fail  bool
}

func (s *recoveryMetricSource) recoveryStatus(context.Context) (RecoveryStatus, error) {
	s.calls++
	if s.fail {
		return RecoveryStatus{}, errors.New("private restore detail")
	}
	return RecoveryStatus{RecoveryID: "private-restore-id", Held: true, Quarantined: 4, ActiveSubmissions: 1}, nil
}

func TestRecoveryMetricsAreGatedPrivateAndOmitFailedInventory(t *testing.T) {
	source := &recoveryMetricSource{}
	var enabled atomic.Bool
	r := prometheus.NewRegistry()
	r.MustRegister(NewRecoveryMonitor(source, enabled.Load))
	metrics, err := r.Gather()
	if err != nil || len(metrics) != 1 || source.calls != 0 {
		t.Fatal("disabled monitor read recovery")
	}
	enabled.Store(true)
	metrics, err = r.Gather()
	if err != nil || len(metrics) != 5 || source.calls != 1 {
		t.Fatal("missing recovery aggregates")
	}
	for _, family := range metrics {
		for _, metric := range family.Metric {
			if len(metric.Label) != 0 || strings.Contains(family.String(), "private-") {
				t.Fatal("private recovery labels")
			}
		}
	}
	source.fail = true
	metrics, err = r.Gather()
	if err != nil || len(metrics) != 2 {
		t.Fatal("failed inventory was exported")
	}
}
