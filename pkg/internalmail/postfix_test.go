package internalmail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	dsnRecipient = "71a25d88-9a8e-425f-9e9b-b28802234faa"
	reportToken  = "separate-report-credential-32-bytes-minimum"
)

func sampleDSN(id, action, status string) []byte {
	return []byte(fmt.Sprintf("From: MAILER-DAEMON@mail.example.test\r\n"+
		"To: bounce+%s@example.test\r\nMIME-Version: 1.0\r\n"+
		"Content-Type: multipart/report; report-type=delivery-status; boundary=report\r\n\r\n"+
		"--report\r\nContent-Type: text/plain; charset=us-ascii\r\n\r\nDelivery report.\r\n"+
		"--report\r\nContent-Type: message/delivery-status\r\n\r\n"+
		"Reporting-MTA: dns; mail.example.test\r\nOriginal-Envelope-Id: gregale-%s\r\n\r\n"+
		"Final-Recipient: rfc822; one@example.net\r\nAction: %s\r\nStatus: %s\r\n"+
		"Diagnostic-Code: smtp; a private provider diagnostic\r\n\r\n"+
		"--report\r\nContent-Type: text/rfc822-headers\r\n\r\nSubject: private subject\r\n\r\n"+
		"--report--\r\n", id, id, action, status))
}

func TestPostfixDSNParsingAndStableIdentity(t *testing.T) {
	for _, tc := range []struct{ action, status, kind string }{
		{"delivered", "2.0.0", "accepted"}, {"relayed", "2.0.0", "accepted"},
		{"delayed", "4.4.1", "deferred"}, {"failed", "5.1.1", "bounced"},
		{"failed", "4.4.7", "bounced"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			raw := sampleDSN(dsnRecipient, tc.action, tc.status)
			for _, data := range [][]byte{raw, bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))} {
				first, err := ParsePostfixReport("bounce+"+dsnRecipient+"@example.test", data)
				if err != nil {
					t.Fatal(err)
				}
				second, err := ParsePostfixReport("bounce+"+dsnRecipient+"@example.test", data)
				if err != nil || first != second || first.EventID == "" {
					t.Fatal("redelivery changed report identity", err)
				}
				if first.Kind != tc.kind || first.Code != "dsn_"+tc.status || first.RecipientID != dsnRecipient || first.RecipientAddress != "one@example.net" {
					t.Fatal(first)
				}
			}
		})
	}
}

func TestPostfixDSNRejectsUncorrelatedOrMalformedReports(t *testing.T) {
	raw := string(sampleDSN(dsnRecipient, "relayed", "2.0.0"))
	for _, tc := range []struct{ name, recipient, body string }{
		{"unrelated-envelope", "person@example.test", raw},
		{"different-recipient", "bounce+81a25d88-9a8e-425f-9e9b-b28802234faa@example.test", raw},
		{"no-envelope-id", "", strings.ReplaceAll(raw, "Original-Envelope-Id", "Ignored-Envelope-Id")},
		{"duplicate-envelope-id", "", strings.ReplaceAll(raw, "Original-Envelope-Id:", "Original-Envelope-Id: unrelated\r\nOriginal-Envelope-Id:")},
		{"duplicate-action", "", strings.ReplaceAll(raw, "Action:", "Action: failed\r\nAction:")},
		{"contradictory-status", "", strings.ReplaceAll(raw, "Status: 2.0.0", "Status: 5.1.1")},
		{"expanded-not-handoff", "", strings.ReplaceAll(raw, "Action: relayed", "Action: expanded")},
		{"invalid-status", "", strings.ReplaceAll(raw, "Status: 2.0.0", "Status: 2.1234.0")},
		{"invalid-address", "", strings.ReplaceAll(raw, "rfc822; one@example.net", "rfc822; invalid")},
		{"multiple-recipients", "", strings.ReplaceAll(raw, "--report\r\nContent-Type: text/rfc822-headers", "Final-Recipient: rfc822; two@example.net\r\nAction: failed\r\nStatus: 5.1.1\r\n\r\n--report\r\nContent-Type: text/rfc822-headers")},
		{"feedback-loop-not-dsn", "", strings.ReplaceAll(raw, "report-type=delivery-status", "report-type=feedback-report")},
		{"encoded-status", "", strings.ReplaceAll(raw, "Content-Type: message/delivery-status", "Content-Type: message/delivery-status\r\nContent-Transfer-Encoding: base64")},
		{"truncated-mime", "", strings.ReplaceAll(raw, "--report--", "")},
		{"oversized", "", strings.Repeat("x", api.InternalMailMaxDSNBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.recipient == "" {
				tc.recipient = "bounce+" + dsnRecipient + "@example.test"
			}
			if _, err := ParsePostfixReport(tc.recipient, []byte(tc.body)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted malformed DSN: %v", err)
			}
		})
	}
}

func TestPostfixRouteGatesAndSeparateCredential(t *testing.T) {
	var enabled, reports atomic.Bool
	s := &Service{Enabled: enabled.Load, ReportsEnabled: reports.Load, ReportToken: reportToken}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, token, path string
		master, reports   bool
		want              int
	}{
		{"master-off", reportToken, PostfixReportPath, false, true, 404},
		{"reports-off", reportToken, PostfixReportPath, true, false, 404},
		{"customer-credential", "customer-token", PostfixReportPath, true, true, 401},
		{"full-token-not-report-token", testToken, PostfixReportPath, true, true, 401},
		{"reports-cannot-send", reportToken, APIPath + "/messages", true, true, 401},
		{"reports-cannot-manually-retry", reportToken, APIPath + "/reports", true, true, 401},
		{"reports-require-null-sender", reportToken, PostfixReportPath, true, true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enabled.Store(tc.master)
			reports.Store(tc.reports)
			r := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(sampleDSN(dsnRecipient, "relayed", "2.0.0")))
			r.Header.Set("Content-Type", "message/rfc822")
			r.Header.Set("Authorization", "Bearer "+tc.token)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("HTTP %d, want %d", w.Code, tc.want)
			}
		})
	}
}

type forbiddenRead struct{ t *testing.T }

func (r forbiddenRead) Read([]byte) (int, error) {
	r.t.Fatal("disabled bridge read mail")
	return 0, io.EOF
}

func TestPostfixBridgeRetainsReportsOnUnavailableOrRejectedAPI(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	socket := filepath.Join(t.TempDir(), "maild.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var status atomic.Int32
	status.Store(http.StatusServiceUnavailable)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PostfixReportPath || r.Header.Get("Authorization") != "Bearer "+reportToken || r.Header.Get(ReportSenderHeader) != "<>" {
			t.Error("invalid report request")
		}
		w.Header().Set("Location", "https://external.example.test")
		w.WriteHeader(int(status.Load()))
	}), ReadHeaderTimeout: api.InternalMailAPITimeout}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	bridge, err := NewPostfixBridge(socket, reportToken, enabled.Load)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []int{400, 401, 404, 409, 413, 429, 500, 503, 302} {
		status.Store(int32(code))
		if err := bridge.Submit(t.Context(), "<>", "bounce+"+dsnRecipient+"@example.test", bytes.NewReader(sampleDSN(dsnRecipient, "relayed", "2.0.0"))); err == nil {
			t.Fatal("acknowledged unrecorded report", code)
		}
	}
	status.Store(200)
	if err := bridge.Submit(t.Context(), "<>", "bounce+"+dsnRecipient+"@example.test", bytes.NewReader(sampleDSN(dsnRecipient, "relayed", "2.0.0"))); err != nil {
		t.Fatal(err)
	}
	enabled.Store(false)
	if err := bridge.Submit(t.Context(), "<>", "bounce+"+dsnRecipient+"@example.test", forbiddenRead{t}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	enabled.Store(true)
	if err := bridge.Submit(t.Context(), "attacker@example.test", "bounce+"+dsnRecipient+"@example.test", forbiddenRead{t}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	_ = server.Close()
	if err := bridge.Submit(context.Background(), "<>", "bounce+"+dsnRecipient+"@example.test", bytes.NewReader([]byte("report"))); err == nil {
		t.Fatal("acknowledged unavailable socket")
	}
}
