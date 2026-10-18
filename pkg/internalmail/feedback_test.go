package internalmail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/onebox-faas/faas/pkg/api"
)

const feedbackToken = "private-feedback-collector-token-at-least-32-bytes"

func feedbackSource() FeedbackSource {
	return FeedbackSource{ID: "provider", Token: feedbackToken, DKIMDomains: []string{"feedback.provider.test"}, ReportFrom: []string{"reports@feedback.provider.test"}, RecipientDomains: []string{"example.net"}, Formats: []string{"dsn", "arf"}}
}

type feedbackSigner struct {
	key      *rsa.PrivateKey
	resolver testResolver
}

func newFeedbackSigner(t *testing.T) feedbackSigner {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return feedbackSigner{key, testResolver{"reports._domainkey.feedback.provider.test": {"v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(public)}}}
}

func (signer feedbackSigner) sign(t *testing.T, raw []byte, domain string, headers []string) []byte {
	t.Helper()
	var signed bytes.Buffer
	if err := dkim.Sign(&signed, bytes.NewReader(raw), &dkim.SignOptions{
		Domain: domain, Selector: "reports", Signer: signer.key, HeaderKeys: headers,
		HeaderCanonicalization: dkim.CanonicalizationRelaxed, BodyCanonicalization: dkim.CanonicalizationRelaxed,
	}); err != nil {
		t.Fatal(err)
	}
	return signed.Bytes()
}

func sampleFeedback(id, domain, to, format, status string) []byte {
	reportType := "delivery-status"
	observation := "Reporting-MTA: dns; mx.provider.test\r\n\r\nFinal-Recipient: rfc822; " + to + "\r\nAction: failed\r\nStatus: " + status + "\r\nDiagnostic-Code: smtp; private provider diagnostic\r\n"
	if format == "arf" {
		reportType = "feedback-report"
		observation = "Feedback-Type: abuse\r\nVersion: 1\r\nUser-Agent: private-test-fbl\r\nOriginal-Mail-From: <bounce+" + id + "@" + domain + ">\r\nOriginal-Rcpt-To: <" + to + ">\r\n"
	}
	return []byte(fmt.Sprintf("From: Provider reports <reports@feedback.provider.test>\r\nTo: postmaster@example.test\r\nMessage-ID: <feedback-%s-%s@feedback.provider.test>\r\nMIME-Version: 1.0\r\nContent-Type: multipart/report; report-type=%s; boundary=feedback-test\r\n\r\n"+
		"--feedback-test\r\nContent-Type: text/plain\r\n\r\nPrivate report explanation\r\n"+
		"--feedback-test\r\nContent-Type: message/%s\r\n\r\n%s\r\n"+
		"--feedback-test\r\nContent-Type: text/rfc822-headers\r\n\r\nFrom: Gregale <notify@%s>\r\nTo: %s\r\nMessage-ID: <%s@%s>\r\nX-Gregale-Mail-Recipient-ID: %s\r\n\r\n"+
		"--feedback-test--\r\n", id, format, reportType, reportType, observation, domain, to, id, domain, id))
}

func TestFeedbackConfigurationFailsClosed(t *testing.T) {
	source := feedbackSource()
	encoded, err := json.Marshal([]FeedbackSource{source})
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]string{EnableEnv: "1", FeedbackEnableEnv: "1", "FAAS_INTERNAL_MAIL_TOKEN": testToken, "FAAS_INTERNAL_MAIL_AGE_IDENTITY_PATH": "/private/mail.age", "FAAS_INTERNAL_MAIL_SENDING_IP": "192.0.2.10", "FAAS_INTERNAL_MAIL_FEEDBACK_SOURCES": string(encoded)}
	get := func(key string) string { return base[key] }
	if cfg, err := LoadConfig(get); err != nil || !cfg.Feedback || len(cfg.FeedbackSources) != 1 {
		t.Fatal("valid source rejected", err)
	}
	for _, flag := range []string{"", "0", "true", "yes", " 1"} {
		base[FeedbackEnableEnv] = flag
		cfg, err := LoadConfig(func(key string) string {
			if key == "FAAS_INTERNAL_MAIL_FEEDBACK_SOURCES" {
				t.Fatal("disabled feedback read credentials")
			}
			return get(key)
		})
		if err != nil || cfg.Feedback || len(cfg.FeedbackSources) != 0 {
			t.Fatal("feedback default is not off", err)
		}
	}
	for _, raw := range []string{"", "null", "[]", string(encoded) + " {}", strings.Replace(string(encoded), `"id":`, `"unknown":`, 1), string(bytes.Repeat([]byte("x"), api.InternalMailMaxFeedbackConfigBytes+1)), "[" + string(encoded[1:len(encoded)-1]) + "," + string(encoded[1:len(encoded)-1]) + "]"} {
		if _, err := parseFeedbackSources(raw, testToken, reportToken); err == nil {
			t.Fatal("invalid source configuration accepted")
		}
	}
	for _, tc := range []struct {
		name   string
		change func(*FeedbackSource)
	}{
		{"path-source", func(s *FeedbackSource) { s.ID = "../provider" }},
		{"long-source", func(s *FeedbackSource) { s.ID = strings.Repeat("x", api.InternalMailMaxFeedbackSourceBytes+1) }},
		{"missing-token", func(s *FeedbackSource) { s.Token = "" }},
		{"service-token", func(s *FeedbackSource) { s.Token = testToken }},
		{"telemetry-token", func(s *FeedbackSource) { s.Token = reportToken }},
		{"missing-signers", func(s *FeedbackSource) { s.DKIMDomains = nil }},
		{"wildcard-signer", func(s *FeedbackSource) { s.DKIMDomains = []string{"*.provider.test"} }},
		{"uppercase-signer", func(s *FeedbackSource) { s.DKIMDomains = []string{"Feedback.provider.test"} }},
		{"missing-report-from", func(s *FeedbackSource) { s.ReportFrom = nil }},
		{"wildcard-report-from", func(s *FeedbackSource) { s.ReportFrom = []string{"*@feedback.provider.test"} }},
		{"unaligned-report-from", func(s *FeedbackSource) { s.ReportFrom = []string{"reports@other.test"} }},
		{"missing-destinations", func(s *FeedbackSource) { s.RecipientDomains = nil }},
		{"wildcard-destination", func(s *FeedbackSource) { s.RecipientDomains = []string{"*.example.net"} }},
		{"no-format", func(s *FeedbackSource) { s.Formats = nil }},
		{"unknown-format", func(s *FeedbackSource) { s.Formats = []string{"json"} }},
		{"repeated-format", func(s *FeedbackSource) { s.Formats = []string{"arf", "arf"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := feedbackSource()
			tc.change(&changed)
			if err := validateFeedbackSources(map[string]FeedbackSource{changed.ID: changed}, testToken, reportToken); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
	second := feedbackSource()
	second.ID = "second"
	if err := validateFeedbackSources(map[string]FeedbackSource{source.ID: source, second.ID: second}, testToken, reportToken); err == nil {
		t.Fatal("sources shared a credential")
	}
}

func TestFeedbackParserRejectsAmbiguousOrUncorrelatedReports(t *testing.T) {
	raw := string(sampleFeedback(dsnRecipient, "example.test", "one@example.net", "dsn", "5.1.1"))
	arf := string(sampleFeedback(dsnRecipient, "example.test", "one@example.net", "arf", ""))
	for _, input := range []string{raw, arf, strings.Replace(raw, "Action: failed\r\nStatus: 5.1.1", "Action: delayed\r\nStatus: 4.4.1", 1), strings.Replace(raw, "Reporting-MTA:", "Original-Envelope-ID: gregale-"+dsnRecipient+"\r\nReporting-MTA:", 1), strings.Replace(raw, "text/rfc822-headers", "message/rfc822", 1)} {
		parsed, err := parseFeedback([]byte(input))
		if err != nil || parsed.report.RecipientID != dsnRecipient || parsed.report.MessageDomain != "example.test" || parsed.report.RecipientAddress != "one@example.net" {
			t.Fatal("valid feedback rejected", err)
		}
	}
	for _, tc := range []struct{ name, input string }{
		{"empty", ""},
		{"truncated", raw[:len(raw)-24]},
		{"missing-original", strings.Replace(raw, "Content-Type: text/rfc822-headers", "Content-Type: text/plain", 1)},
		{"duplicate-outer-from", "From: other@feedback.provider.test\r\n" + raw},
		{"duplicate-outer-identity", "Message-ID: <other@feedback.provider.test>\r\n" + raw},
		{"unsigned-auth-results-only", "Authentication-Results: mx; dkim=pass\r\n" + strings.Replace(raw, "MIME-Version: 1.0", "MIME-Version: 2.0", 1)},
		{"duplicate-content-type", "Content-Type: text/plain\r\n" + raw},
		{"missing-recipient-id", strings.Replace(raw, "X-Gregale-Mail-Recipient-ID: "+dsnRecipient, "X-Unrelated: ignored", 1)},
		{"different-recipient-id", strings.Replace(raw, "X-Gregale-Mail-Recipient-ID: "+dsnRecipient, "X-Gregale-Mail-Recipient-ID: 81a25d88-9a8e-425f-9e9b-b28802234faa", 1)},
		{"wrong-original-from", strings.Replace(raw, "notify@example.test", "notify@another.test", 1)},
		{"different-final-recipient", strings.Replace(raw, "Final-Recipient: rfc822; one@example.net", "Final-Recipient: rfc822; two@example.net", 1)},
		{"multiple-dsn-recipients", strings.Replace(raw, "Diagnostic-Code:", "\r\nFinal-Recipient: rfc822; two@example.net\r\nAction: failed\r\nStatus: 5.1.1\r\nDiagnostic-Code:", 1)},
		{"duplicate-dsn-status", strings.Replace(raw, "Status: 5.1.1", "Status: 5.1.1\r\nStatus: 4.4.1", 1)},
		{"wrong-envid", strings.Replace(raw, "Reporting-MTA:", "Original-Envelope-ID: gregale-81a25d88-9a8e-425f-9e9b-b28802234faa\r\nReporting-MTA:", 1)},
		{"empty-envid", strings.Replace(raw, "Reporting-MTA:", "Original-Envelope-ID: \r\nReporting-MTA:", 1)},
		{"success-report", strings.Replace(raw, "Action: failed\r\nStatus: 5.1.1", "Action: delivered\r\nStatus: 2.0.0", 1)},
		{"different-original-recipient", strings.Replace(raw, "Final-Recipient:", "Original-Recipient: rfc822; two@example.net\r\nFinal-Recipient:", 1)},
		{"noncomplaint-arf", strings.Replace(arf, "Feedback-Type: abuse", "Feedback-Type: not-spam", 1)},
		{"multiple-arf-recipients", strings.Replace(arf, "Original-Rcpt-To:", "Original-Rcpt-To: <two@example.net>\r\nOriginal-Rcpt-To:", 1)},
		{"wrong-arf-envelope", strings.Replace(arf, "Original-Mail-From: <bounce+", "Original-Mail-From: <other+", 1)},
		{"oversized-original-headers", strings.Replace(raw, "X-Gregale-Mail-Recipient-ID: "+dsnRecipient+"\r\n", "X-Gregale-Mail-Recipient-ID: "+dsnRecipient+"\r\nX-Padding: "+strings.Repeat("x", api.InternalMailMaxDSNStatusBytes)+"\r\n", 1)},
		{"oversized-report", strings.Repeat("x", api.InternalMailMaxFeedbackBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseFeedback([]byte(tc.input)); !errors.Is(err, ErrInvalid) {
				t.Fatal("ambiguous feedback accepted", err)
			}
		})
	}
}

func TestFeedbackRequiresApprovedDKIMAndSignedCorrelationHeaders(t *testing.T) {
	signer := newFeedbackSigner(t)
	source := feedbackSource()
	s := &Service{Enabled: func() bool { return true }, FeedbackEnabled: func() bool { return true }, FeedbackSources: map[string]FeedbackSource{source.ID: source}, Resolver: signer.resolver}
	raw := sampleFeedback(dsnRecipient, "example.test", "one@example.net", "dsn", "5.1.1")
	signed := signer.sign(t, raw, source.DKIMDomains[0], nil)
	if err := s.authenticateFeedback(t.Context(), source, "reports@feedback.provider.test", signed); err != nil {
		t.Fatal("approved signature rejected", err)
	}
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"unsigned", raw},
		{"forged-authentication-results", append([]byte("Authentication-Results: provider.test; dkim=pass\r\n"), raw...)},
		{"tampered-body", bytes.Replace(signed, []byte("Status: 5.1.1"), []byte("Status: 5.2.1"), 1)},
		{"unapproved-signer", signer.sign(t, raw, "attacker.test", nil)},
		{"unapproved-mailbox-on-approved-domain", signer.sign(t, bytes.Replace(raw, []byte("reports@feedback.provider.test"), []byte("user@feedback.provider.test"), 1), source.DKIMDomains[0], nil)},
		{"unprotected-content-type", signer.sign(t, raw, source.DKIMDomains[0], []string{"From", "Message-ID", "MIME-Version"})},
		{"unprotected-identity", signer.sign(t, raw, source.DKIMDomains[0], []string{"From", "Content-Type", "MIME-Version"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.IngestFeedback(t.Context(), source.ID, tc.raw); !errors.Is(err, ErrFeedbackUntrusted) {
				t.Fatal("unauthenticated feedback reached storage", err)
			}
		})
	}
	if err := s.authenticateFeedback(t.Context(), source, "reports@other.test", signed); !errors.Is(err, ErrFeedbackUntrusted) {
		t.Fatal("misaligned report From accepted", err)
	}
	other := source
	other.RecipientDomains = []string{"other.test"}
	s.FeedbackSources[source.ID] = other
	if err := s.IngestFeedback(t.Context(), source.ID, signed); !errors.Is(err, ErrFeedbackUntrusted) {
		t.Fatal("source affected another recipient domain", err)
	}
	other = source
	other.Formats = []string{"arf"}
	s.FeedbackSources[source.ID] = other
	if err := s.IngestFeedback(t.Context(), source.ID, signed); !errors.Is(err, ErrFeedbackUntrusted) {
		t.Fatal("source used an unenrolled format", err)
	}
	if err := s.IngestFeedback(t.Context(), "unknown", signed); !errors.Is(err, ErrFeedbackUntrusted) {
		t.Fatal("unenrolled source accepted", err)
	}
}

type feedbackResolverFunc func(context.Context, string) ([]string, error)

func (f feedbackResolverFunc) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return f(ctx, name)
}

func TestFeedbackDNSFailureAndGateRevocationCannotUpdateState(t *testing.T) {
	signer := newFeedbackSigner(t)
	source := feedbackSource()
	var enabled atomic.Bool
	enabled.Store(true)
	s := &Service{Enabled: func() bool { return true }, FeedbackEnabled: enabled.Load, FeedbackSources: map[string]FeedbackSource{source.ID: source}}
	raw := signer.sign(t, sampleFeedback(dsnRecipient, "example.test", "one@example.net", "arf", ""), source.DKIMDomains[0], nil)
	s.Resolver = feedbackResolverFunc(func(context.Context, string) ([]string, error) {
		return nil, &net.DNSError{Err: "temporary private diagnostic", IsTemporary: true}
	})
	if err := s.IngestFeedback(t.Context(), source.ID, raw); !errors.Is(err, ErrFeedbackUnavailable) {
		t.Fatal("transient DNS failure not retryable", err)
	}
	s.Resolver = feedbackResolverFunc(func(ctx context.Context, name string) ([]string, error) {
		enabled.Store(false)
		return signer.resolver.LookupTXT(ctx, name)
	})
	if err := s.IngestFeedback(t.Context(), source.ID, raw); !errors.Is(err, ErrDisabled) {
		t.Fatal("gate revocation reached storage", err)
	}
	s.Resolver = nil
	if err := s.IngestFeedback(t.Context(), source.ID, raw); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled path parsed feedback", err)
	}
}

func TestFeedbackDoesNotQueryUnapprovedSignersAndBoundsVerificationWork(t *testing.T) {
	signer := newFeedbackSigner(t)
	source := feedbackSource()
	s := &Service{Enabled: func() bool { return true }, FeedbackEnabled: func() bool { return true }, FeedbackSources: map[string]FeedbackSource{source.ID: source}}
	raw := sampleFeedback(dsnRecipient, "example.test", "one@example.net", "dsn", "5.1.1")
	s.Resolver = feedbackResolverFunc(func(context.Context, string) ([]string, error) {
		t.Fatal("queried DNS for a signer outside the enrolled domains")
		return nil, ErrFeedbackUntrusted
	})
	if err := s.IngestFeedback(t.Context(), source.ID, signer.sign(t, raw, "attacker.test", nil)); !errors.Is(err, ErrFeedbackUntrusted) {
		t.Fatal("unapproved signer reached state", err)
	}
	var lookups atomic.Int32
	s.Resolver = feedbackResolverFunc(func(ctx context.Context, name string) ([]string, error) {
		lookups.Add(1)
		return signer.resolver.LookupTXT(ctx, name)
	})
	for range api.InternalMailMaxFeedbackSignatures + 1 {
		raw = signer.sign(t, raw, source.DKIMDomains[0], nil)
	}
	if err := s.IngestFeedback(t.Context(), source.ID, raw); !errors.Is(err, ErrFeedbackUntrusted) {
		t.Fatal("excess signatures reached state", err)
	}
	if lookups.Load() > api.InternalMailMaxFeedbackSignatures {
		t.Fatal("unbounded DKIM verification work", lookups.Load())
	}
}

func TestFeedbackRouteCredentialsAreIsolatedAndDefaultOff(t *testing.T) {
	var master, feedback atomic.Bool
	master.Store(true)
	feedback.Store(true)
	source := feedbackSource()
	s := &Service{Enabled: master.Load, FeedbackEnabled: feedback.Load, FeedbackSources: map[string]FeedbackSource{source.ID: source}, ReportsEnabled: func() bool { return true }, ReportToken: reportToken}
	second := feedbackSource()
	second.ID, second.Token = "second", "a-different-private-feedback-source-credential"
	s.FeedbackSources[second.ID] = second
	handler, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path, token string
		master, feedback  bool
		want              int
	}{
		{"disabled-master", FeedbackPath + "/provider", feedbackToken, false, true, 404},
		{"disabled-feedback", FeedbackPath + "/provider", feedbackToken, true, false, 404},
		{"unknown-source", FeedbackPath + "/unknown", feedbackToken, true, true, 404},
		{"source-cannot-use-another-feed", FeedbackPath + "/second", feedbackToken, true, true, 401},
		{"service-cannot-ingest", FeedbackPath + "/provider", testToken, true, true, 401},
		{"telemetry-cannot-ingest", FeedbackPath + "/provider", reportToken, true, true, 401},
		{"source-cannot-send", APIPath + "/messages", feedbackToken, true, true, 401},
		{"source-cannot-report", APIPath + "/reports", feedbackToken, true, true, 401},
		{"source-cannot-access-postfix", PostfixReportPath, feedbackToken, true, true, 401},
		{"source-cannot-monitor", MonitoringPath, feedbackToken, true, true, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			master.Store(tc.master)
			feedback.Store(tc.feedback)
			r := httptest.NewRequest(http.MethodPost, tc.path, forbiddenRead{t})
			r.Header.Set("Authorization", "Bearer "+tc.token)
			r.Header.Set("Content-Type", "message/rfc822")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatal(w.Code, "want", tc.want)
			}
		})
	}
	master.Store(true)
	feedback.Store(true)
	for _, tc := range []struct {
		input, contentType string
		want               int
	}{
		{"{}", "application/json", 415},
		{"malformed", "message/rfc822", 400},
		{strings.Repeat("x", api.InternalMailMaxFeedbackBytes+1), "message/rfc822", 413},
		{string(sampleFeedback(dsnRecipient, "example.test", "one@example.net", "dsn", "5.1.1")), "message/rfc822", 503},
	} {
		r := httptest.NewRequest(http.MethodPost, FeedbackPath+"/provider", strings.NewReader(tc.input))
		r.Header.Set("Authorization", "Bearer "+feedbackToken)
		r.Header.Set("Content-Type", tc.contentType)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want || strings.Contains(w.Body.String(), "Private report") || strings.Contains(w.Body.String(), feedbackToken) {
			t.Fatal("unsafe feedback response", w.Code, tc.want)
		}
	}
}
