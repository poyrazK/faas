package internalmail

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Handler never registers anything on apid or the public gateway. Callers must
// serve it exclusively over the protected maild Unix socket.
func Handler(s *Service, token string) (http.Handler, error) {
	if s == nil || len(token) < api.InternalMailTokenMinBytes {
		return nil, ErrInvalid
	}
	if s.reportsEnabled() && (!validToken(s.ReportToken) || s.ReportToken == token) {
		return nil, ErrInvalid
	}
	if s.backpressureEnabled() && !s.reportsEnabled() {
		return nil, ErrInvalid
	}
	if s.eventFeedEnabled() && !s.producersEnabled() {
		return nil, ErrInvalid
	}
	if s.producerLifecycleEnabled() && !s.producersEnabled() {
		return nil, ErrInvalid
	}
	if s.reputationEnabled() && !s.feedbackEnabled() {
		return nil, ErrInvalid
	}
	if s.feedbackEnabled() {
		if err := validateFeedbackSources(s.FeedbackSources, token, s.ReportToken); err != nil {
			return nil, err
		}
	}
	if s.operationsEnabled() {
		if err := validateOperationsToken(s.OperationsToken, token, s.ReportToken, s.FeedbackSources); err != nil {
			return nil, err
		}
	}
	if s.producersEnabled() {
		if err := validateProducers(s.Producers, token, s.ReportToken, s.OperationsToken, s.FeedbackSources); err != nil {
			return nil, err
		}
	}
	mux := http.NewServeMux()
	if s.Metrics != nil {
		mux.Handle("GET /metrics", s.Metrics)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.Ready == nil || s.Ready(r.Context()) != nil {
			writeProblem(w, http.StatusServiceUnavailable, "not_ready")
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST "+APIPath+"/domains", s.postDomain)
	mux.HandleFunc("GET "+APIPath+"/domains/{id}", s.getDomain)
	mux.HandleFunc("POST "+APIPath+"/domains/{id}/verify", s.postVerify)
	mux.HandleFunc("POST "+APIPath+"/domains/{id}/disable", s.postDisable)
	mux.HandleFunc("POST "+APIPath+"/messages", s.postMessage)
	mux.HandleFunc("GET "+APIPath+"/messages/{id}", s.getMessage)
	mux.HandleFunc("GET "+APIPath+"/messages/{id}/events", s.getEvents)
	mux.HandleFunc("POST "+APIPath+"/reports", s.postReport)
	mux.HandleFunc("POST "+PostfixReportPath, s.postPostfixReport)
	s.registerRelayHealth(mux)
	s.registerReputation(mux)
	s.registerEventFeed(mux)
	s.registerProducerScheduler(mux)
	s.registerProducerLifecycle(mux)
	s.registerEncryption(mux)
	s.registerRecovery(mux)
	s.registerProducerOutbox(mux)
	s.registerSuppressionControls(mux)
	mux.HandleFunc("POST "+FeedbackPath+"/{source}", s.postFeedback)
	mux.HandleFunc("POST "+RetentionPath, s.postRetention)
	s.registerOperations(mux)
	s.registerProducers(mux)
	if s.Monitoring != nil {
		mux.Handle("GET "+MonitoringPath, s.Monitoring)
	}
	expected := sha256.Sum256([]byte("Bearer " + token))
	reportExpected := sha256.Sum256([]byte("Bearer " + s.ReportToken))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !s.enabled() {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == RetentionPath && !s.retentionEnabled() {
			http.NotFound(w, r)
			return
		}
		if eventFeedRoute(r.URL.Path) && !s.eventFeedEnabled() {
			http.NotFound(w, r)
			return
		}
		if producerLifecycleRoute(r.URL.Path) && !s.producerLifecycleEnabled() {
			http.NotFound(w, r)
			return
		}
		if (r.URL.Path == ProducerOutboxPath || strings.HasPrefix(r.URL.Path, ProducerOutboxPath+"/")) && !s.producerOutboxEnabled() {
			http.NotFound(w, r)
			return
		}
		if producerOutboxAdmissionRoute(r.URL.Path) && !s.outboxReconciliationEnabled() {
			http.NotFound(w, r)
			return
		}
		if recoveryRoute(r.URL.Path) && !s.recoveryEnabled() {
			http.NotFound(w, r)
			return
		}
		if encryptionRoute(r.URL.Path) && !s.encryptionRotationEnabled() {
			http.NotFound(w, r)
			return
		}
		if suppressionControlsRoute(r.URL.Path) && !s.suppressionControlsEnabled() {
			http.NotFound(w, r)
			return
		}
		if (r.URL.Path == ProducerSchedulerPath || strings.HasPrefix(r.URL.Path, ProducerSchedulerPath+"/")) && !s.producerSchedulingEnabled() {
			http.NotFound(w, r)
			return
		}
		if s.domainRouteDisabled(r.URL.Path) || s.controlRouteDisabled(r.URL.Path) || s.relayRouteDisabled(r.URL.Path) || s.reputationRouteDisabled(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), api.InternalMailAPITimeout)
		defer cancel()
		r = r.WithContext(ctx)
		if producerRoute(r.URL.Path) {
			if authenticated, ok := s.authenticateProducer(w, r); ok {
				mux.ServeHTTP(w, authenticated)
			}
			return
		}
		credential := expected
		if r.URL.Path == OperationsPath || strings.HasPrefix(r.URL.Path, OperationsPath+"/") {
			if !s.operationsEnabled() || !validToken(s.OperationsToken) {
				http.NotFound(w, r)
				return
			}
			credential = sha256.Sum256([]byte("Bearer " + s.OperationsToken))
		}
		if r.URL.Path == PostfixReportPath || r.URL.Path == MonitoringPath || r.URL.Path == RelayHealthPath {
			if !s.reportsEnabled() || !validToken(s.ReportToken) {
				http.NotFound(w, r)
				return
			}
			credential = reportExpected
		}
		if strings.HasPrefix(r.URL.Path, FeedbackPath+"/") {
			source, ok := s.FeedbackSources[strings.TrimPrefix(r.URL.Path, FeedbackPath+"/")]
			if !s.feedbackEnabled() || !ok || !validToken(source.Token) {
				http.NotFound(w, r)
				return
			}
			credential = sha256.Sum256([]byte("Bearer " + source.Token))
		}
		actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if len(r.Header.Values("Authorization")) != 1 || subtle.ConstantTimeCompare(actual[:], credential[:]) != 1 {
			writeProblem(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		mux.ServeHTTP(w, r)
	}), nil
}

func (s *Service) postDomain(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &input) {
		return
	}
	d, err := s.RegisterDomain(r.Context(), input.Name)
	respond(w, d, err, http.StatusCreated)
}
func (s *Service) getDomain(w http.ResponseWriter, r *http.Request) {
	if !validID(w, r) {
		return
	}
	d, err := s.Store.Domain(r.Context(), r.PathValue("id"))
	respond(w, s.domainResponse(d), err, http.StatusOK)
}
func (s *Service) postVerify(w http.ResponseWriter, r *http.Request) {
	if !validID(w, r) {
		return
	}
	d, err := s.VerifyDomain(r.Context(), r.PathValue("id"))
	respond(w, d, err, http.StatusOK)
}
func (s *Service) postDisable(w http.ResponseWriter, r *http.Request) {
	if !validID(w, r) {
		return
	}
	err := s.Store.SetDomainStatus(r.Context(), r.PathValue("id"), "disabled")
	respond(w, map[string]string{"status": "disabled"}, err, http.StatusOK)
}
func (s *Service) postMessage(w http.ResponseWriter, r *http.Request) {
	var input SendRequest
	if !decode(w, r, &input) {
		return
	}
	if len(r.Header.Values("Idempotency-Key")) != 1 {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	m, err := s.Submit(r.Context(), r.Header.Get("Idempotency-Key"), input)
	respond(w, m, err, http.StatusAccepted)
}
func (s *Service) getMessage(w http.ResponseWriter, r *http.Request) {
	if !validID(w, r) {
		return
	}
	m, err := s.Store.Message(r.Context(), r.PathValue("id"))
	respond(w, m, err, http.StatusOK)
}
func (s *Service) getEvents(w http.ResponseWriter, r *http.Request) {
	if !validID(w, r) {
		return
	}
	events, err := s.Store.Events(r.Context(), r.PathValue("id"))
	respond(w, map[string]any{"events": events}, err, http.StatusOK)
}
func (s *Service) postReport(w http.ResponseWriter, r *http.Request) {
	var report Report
	if !decode(w, r, &report) {
		return
	}
	if err := validateReport(report); err != nil {
		respond(w, nil, err, 0)
		return
	}
	err := s.Store.Report(r.Context(), report)
	respond(w, map[string]string{"status": "recorded"}, err, http.StatusOK)
}

func (s *Service) postPostfixReport(w http.ResponseWriter, r *http.Request) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "message/rfc822" {
		writeProblem(w, http.StatusUnsupportedMediaType, "dsn_required")
		return
	}
	if len(r.Header.Values(ReportSenderHeader)) != 1 || r.Header.Get(ReportSenderHeader) != "<>" || len(r.Header.Values(ReportRecipientHeader)) != 1 {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, api.InternalMailMaxDSNBytes))
	if err != nil {
		decodeProblem(w, err)
		return
	}
	report, err := ParsePostfixReport(r.Header.Get(ReportRecipientHeader), raw)
	if err == nil {
		report.suppressionSource = suppressionSource{kind: "postfix"}
		err = s.Store.Report(r.Context(), report)
	}
	respond(w, map[string]string{"status": "recorded"}, err, http.StatusOK)
}

func (s *Service) postFeedback(w http.ResponseWriter, r *http.Request) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if len(r.Header.Values("Content-Type")) != 1 || err != nil || media != "message/rfc822" {
		writeProblem(w, http.StatusUnsupportedMediaType, "feedback_mime_required")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, api.InternalMailMaxFeedbackBytes))
	if err != nil {
		decodeProblem(w, err)
		return
	}
	err = s.IngestFeedback(r.Context(), r.PathValue("source"), raw)
	respond(w, map[string]string{"status": "recorded"}, err, http.StatusOK)
}

func (s *Service) postRetention(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DryRun *bool `json:"dry_run"`
	}
	if !decode(w, r, &input) {
		return
	}
	dryRun := input.DryRun == nil || *input.DryRun
	result, err := s.RetainPayloads(r.Context(), dryRun)
	respond(w, result, err, http.StatusOK)
}

func validID(w http.ResponseWriter, r *http.Request) bool {
	if _, err := uuid.Parse(r.PathValue("id")); err != nil {
		respond(w, nil, ErrInvalid, 0)
		return false
	}
	return true
}

func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "json_required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.InternalMailMaxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		decodeProblem(w, err)
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		decodeProblem(w, err)
		return false
	}
	return true
}
func decodeProblem(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeProblem(w, http.StatusRequestEntityTooLarge, "body_too_large")
		return
	}
	writeProblem(w, http.StatusBadRequest, "invalid_json")
}
func respond(w http.ResponseWriter, value any, err error, status int) {
	if err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
		return
	}
	switch {
	case errors.Is(err, ErrRecoveryHeld):
		writeProblem(w, http.StatusServiceUnavailable, "recovery_held")
	case errors.Is(err, ErrRecoveryRevision):
		writeProblem(w, http.StatusConflict, "recovery_revision_changed")
	case errors.Is(err, ErrSuppressionRevision):
		writeProblem(w, http.StatusConflict, "suppression_revision_changed")
	case errors.Is(err, ErrSuppressionProtected):
		writeProblem(w, http.StatusConflict, "complaint_suppression_protected")
	case errors.Is(err, ErrEncryptionRevision):
		writeProblem(w, http.StatusConflict, "encryption_revision_changed")
	case errors.Is(err, ErrEncryptionKeyRequired):
		writeProblem(w, http.StatusConflict, "encryption_key_still_required")
	case errors.Is(err, ErrEncryptionKeyUnavailable):
		writeProblem(w, http.StatusServiceUnavailable, "encryption_key_unavailable")
	case errors.Is(err, ErrProducerCredential):
		writeProblem(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, ErrProducerSuspended):
		writeProblem(w, http.StatusForbidden, "producer_suspended")
	case errors.Is(err, ErrProducerRevision):
		writeProblem(w, http.StatusConflict, "producer_access_revision_changed")
	case errors.Is(err, ErrDisabled):
		writeProblem(w, http.StatusNotFound, "not_found")
	case errors.Is(err, ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, ErrFeedbackUntrusted):
		writeProblem(w, http.StatusUnprocessableEntity, "feedback_untrusted")
	case errors.Is(err, ErrFeedbackUnavailable):
		writeProblem(w, http.StatusServiceUnavailable, "feedback_verification_unavailable")
	case errors.Is(err, ErrRetentionDryRun):
		writeProblem(w, http.StatusConflict, "retention_dry_run_required")
	case errors.Is(err, ErrKeyPropagation):
		writeProblem(w, http.StatusConflict, "dkim_propagation_wait")
	case errors.Is(err, ErrKeyOverlap):
		writeProblem(w, http.StatusConflict, "dkim_selector_still_required")
	case errors.Is(err, ErrNotFound):
		writeProblem(w, http.StatusNotFound, "not_found")
	case errors.Is(err, ErrUnverified):
		writeProblem(w, http.StatusConflict, "domain_unverified")
	case errors.Is(err, ErrNotAllowed):
		writeProblem(w, http.StatusForbidden, "recipient_not_allowed")
	case errors.Is(err, ErrProducerSender):
		writeProblem(w, http.StatusForbidden, "producer_sender_not_allowed")
	case errors.Is(err, ErrConflict):
		writeProblem(w, http.StatusConflict, "conflict")
	case errors.Is(err, ErrControlRevision):
		writeProblem(w, http.StatusConflict, "delivery_control_revision_changed")
	case errors.Is(err, ErrReputationRevision):
		writeProblem(w, http.StatusConflict, "reputation_revision_changed")
	case errors.Is(err, ErrEventFeedGeneration):
		writeProblem(w, http.StatusConflict, "event_consumer_generation_changed")
	case errors.Is(err, ErrQuota):
		writeProblem(w, http.StatusTooManyRequests, "internal_safeguard")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "internal_mail_unavailable")
	}
}
func writeProblem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code})
}
