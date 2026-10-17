package internalmail

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

func TestPGRelayHealthHTTPAuthorityAndStrictPayload(t *testing.T) {
	f := relayFixture(t)
	h, err := Handler(f.service, testToken)
	if err != nil {
		t.Fatal(err)
	}
	input := healthyObservation(time.Now())
	raw, _ := json.Marshal(input)
	call := func(method, path, token string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, token := range []string{testToken, f.service.OperationsToken, producerToken, "invalid"} {
		if w := call(http.MethodPost, RelayHealthPath, token, raw); w.Code != http.StatusUnauthorized {
			t.Fatal("untrusted role published health", w.Code)
		}
	}
	if w := call(http.MethodPost, RelayHealthPath, reportToken, raw); w.Code != http.StatusOK {
		t.Fatal("telemetry role could not publish", w.Code, w.Body.String())
	}
	for _, token := range []string{testToken, reportToken, producerToken} {
		if w := call(http.MethodGet, RelayOperationsPath, token, nil); w.Code != http.StatusUnauthorized {
			t.Fatal("non-operator read relay metadata", w.Code)
		}
	}
	if w := call(http.MethodGet, RelayOperationsPath, f.service.OperationsToken, nil); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "sealed") || strings.Contains(w.Body.String(), "@example") {
		t.Fatal("operator relay status unavailable or leaked", w.Code)
	}
	for _, bad := range [][]byte{[]byte(`{"id":"not-a-uuid"}`), []byte(`{"inventory_ok":true}`), append(append([]byte{}, raw...), []byte(`{}`)...)} {
		if w := call(http.MethodPost, RelayHealthPath, reportToken, bad); w.Code != http.StatusBadRequest {
			t.Fatal("partial or trailing health accepted", w.Code)
		}
	}
	oversize := append(append([]byte{}, raw...), []byte(strings.Repeat(" ", api.InternalMailMaxRelayHealthBytes))...)
	if w := call(http.MethodPost, RelayHealthPath, reportToken, oversize); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatal("unbounded health accepted", w.Code)
	}
	f.service.BackpressureEnabled = func() bool { return false }
	for _, path := range []string{RelayHealthPath, RelayOperationsPath, RelayOperationsPath + "/events"} {
		if w := call(http.MethodPost, path, reportToken, raw); w.Code != http.StatusNotFound {
			t.Fatal("disabled health route exposed", path, w.Code)
		}
	}
}

type relayStatusFunc func(context.Context) (RelayStatus, error)

func (f relayStatusFunc) relayStatus(ctx context.Context) (RelayStatus, error) { return f(ctx) }

func TestRelayHealthMonitorDefaultOffClosedReasonsAndFailure(t *testing.T) {
	var enabled, failed atomic.Bool
	calls := 0
	r := prometheus.NewRegistry()
	r.MustRegister(NewRelayHealthMonitor(relayStatusFunc(func(context.Context) (RelayStatus, error) {
		calls++
		if failed.Load() {
			return RelayStatus{}, errors.New("secret relay diagnostic")
		}
		return RelayStatus{Held: true, Reason: "smtp_unavailable", Fresh: true, Observation: &RelayObservation{ID: "secret", ObservedAt: time.Now(), InventoryOK: true, QueueMessages: 4}}, nil
	}), enabled.Load))
	body := scrapeMonitor(t, r)
	if calls != 0 || !strings.Contains(body, "maild_mail_relay_enabled 0") || strings.Contains(body, "hold_reason") {
		t.Fatal("disabled health monitor read state")
	}
	enabled.Store(true)
	body = scrapeMonitor(t, r)
	if !strings.Contains(body, `maild_mail_relay_hold_reason{reason="smtp_unavailable"} 1`) || !strings.Contains(body, "maild_mail_relay_queue_messages 4") || strings.Contains(body, "secret") {
		t.Fatal("health metrics missing or leaked", body)
	}
	failed.Store(true)
	body = scrapeMonitor(t, r)
	if !strings.Contains(body, "maild_mail_relay_stats_up 0") || strings.Contains(body, "hold_reason") || strings.Contains(body, "secret") {
		t.Fatal("failed health monitor retained healthy data")
	}
}

func TestRelayHealthExporterReportsFailuresAndWholeRelayQueue(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	observations := make(chan RelayObservation, 4)
	var reject atomic.Bool
	socket := filepath.Join(t.TempDir(), "maild.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+reportToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == RelayHealthPath {
			if reject.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			var o RelayObservation
			if json.NewDecoder(r.Body).Decode(&o) != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			observations <- o
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte("maild_mail_enabled 1\nmaild_mail_stats_up 1\n"))
	})}
	go func() { _ = server.Serve(l) }()
	t.Cleanup(func() { _ = server.Close() })
	b, err := NewPostfixBridge(socket, reportToken, enabled.Load)
	if err != nil {
		t.Fatal(err)
	}
	b, err = b.WithRelayHealth(enabled.Load, "127.0.0.1:2525")
	if err != nil {
		t.Fatal(err)
	}
	probes := 0
	b.probe = func(context.Context, string) error { probes++; return errors.New("private SMTP diagnostic") }
	b.queues = func(context.Context) (RelayQueues, error) {
		return RelayQueues{Total: StateStat{Count: 12, OldestSeconds: 300}}, nil
	}
	output := filepath.Join(t.TempDir(), "mail.prom")
	if err := b.ExportMonitoring(t.Context(), output); err != nil {
		t.Fatal(err)
	}
	o := <-observations
	if !o.InventoryOK || o.SMTPUp || o.QueueMessages != 12 || o.OldestSeconds != 300 || probes != 1 {
		t.Fatal("health report lost negative probe or whole queue totals", o)
	}
	b.probe = func(context.Context, string) error { return nil }
	b.queues = func(context.Context) (RelayQueues, error) {
		return RelayQueues{}, errors.New("private inventory diagnostic")
	}
	if err := b.ExportMonitoring(t.Context(), output); err != nil {
		t.Fatal(err)
	}
	o = <-observations
	if o.InventoryOK || !o.SMTPUp || o.QueueMessages != 0 || o.OldestSeconds != 0 {
		t.Fatal("inventory failure appeared healthy", o)
	}
	reject.Store(true)
	if err := b.ExportMonitoring(t.Context(), output); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("failed health publication not surfaced safely", err)
	}
	body, err := os.ReadFile(output)
	if err != nil || !bytes.Contains(body, []byte("maild_mail_monitoring_up 0")) || bytes.Contains(body, []byte("private")) {
		t.Fatal("publication failure retained healthy monitoring", err)
	}
	enabled.Store(false)
	b.queues = func(context.Context) (RelayQueues, error) {
		t.Fatal("disabled collector inspected relay")
		return RelayQueues{}, nil
	}
	if err := b.ExportMonitoring(t.Context(), output); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
}

func TestRelaySMTPProbeNeverSendsEnvelopeOrBody(t *testing.T) {
	for _, address := range []string{"mail.external.test:25", "192.0.2.10:25", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:smtp"} {
		if err := probeRelaySMTP(t.Context(), address); !errors.Is(err, ErrInvalid) {
			t.Fatal("probe accepted non-loopback target", address, err)
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	done := make(chan error, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = c.Close() }()
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = fmt.Fprint(c, "220 fixture\r\n")
		r := bufio.NewReader(c)
		line, err := r.ReadString('\n')
		if err != nil || !strings.HasPrefix(line, "EHLO ") {
			done <- fmt.Errorf("unexpected greeting: %q", line)
			return
		}
		_, _ = fmt.Fprint(c, "250 fixture\r\n")
		line, err = r.ReadString('\n')
		if err != nil || line != "QUIT\r\n" {
			done <- fmt.Errorf("probe sent envelope or body: %q", line)
			return
		}
		_, _ = fmt.Fprint(c, "221 done\r\n")
		done <- nil
	}()
	if err := probeRelaySMTP(t.Context(), l.Addr().String()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRelaySMTPProbeHonorsCancellationBeforeGreeting(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	done := make(chan error, 1)
	accepted := make(chan struct{})
	go func() {
		c, err := l.Accept()
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = c.Close() }()
		_ = c.SetDeadline(time.Now().Add(time.Second))
		close(accepted)
		var b [1]byte
		_, err = c.Read(b[:])
		done <- err
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	probeDone := make(chan error, 1)
	go func() { probeDone <- probeRelaySMTP(ctx, l.Addr().String()) }()
	select {
	case <-accepted:
	case <-ctx.Done():
		t.Fatal("probe never connected")
	}
	cancel()
	select {
	case err := <-probeDone:
		if err == nil {
			t.Fatal("cancelled probe appeared healthy")
		}
	case <-time.After(time.Second):
		t.Fatal("probe ignored cancellation")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled probe retained connection")
	}
}
