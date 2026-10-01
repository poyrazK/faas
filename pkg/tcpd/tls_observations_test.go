package tcpd

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type cancellationCertificateProvider struct {
	cancel      context.CancelFunc
	certificate *tls.Certificate
}

func (p cancellationCertificateProvider) Certificate(context.Context, string) (*tls.Certificate, error) {
	p.cancel()
	return p.certificate, nil
}

func TestTLSCertificateObservationRejectsCanceledLookup(t *testing.T) {
	certificate := testListenerCertificate(t, "echo.example", time.Now().Add(time.Hour))
	if expiry := observeTLSCertificateExpiry(t.Context(), &testCertificateProvider{certificate: certificate}, "echo.example"); expiry.IsZero() {
		t.Fatal("usable certificate was not observed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if expiry := observeTLSCertificateExpiry(ctx, cancellationCertificateProvider{cancel: cancel, certificate: certificate}, "echo.example"); !expiry.IsZero() {
		t.Fatal("canceled provider lookup advertised readiness")
	}
}

func TestTLSCertificateObservationRejectsNoSignatureAlgorithms(t *testing.T) {
	certificate := testListenerCertificate(t, "echo.example", time.Now().Add(time.Hour))
	certificate.SupportedSignatureAlgorithms = []tls.SignatureScheme{}
	if expiry := observeTLSCertificateExpiry(t.Context(), &testCertificateProvider{certificate: certificate}, "echo.example"); !expiry.IsZero() {
		t.Fatal("certificate with no permitted signature algorithms advertised readiness")
	}
}

type testTLSObservationSink struct {
	writes []state.TCPListenerTLSObservation
	prunes []time.Time
	put    func(context.Context) error
}

func (s *testTLSObservationSink) PutTCPListenerTLSObservation(ctx context.Context, observation state.TCPListenerTLSObservation) error {
	if s.put != nil {
		if err := s.put(ctx); err != nil {
			return err
		}
	}
	s.writes = append(s.writes, observation)
	return nil
}

func (s *testTLSObservationSink) PruneTCPListenerTLSObservations(_ context.Context, before time.Time) (int64, error) {
	s.prunes = append(s.prunes, before)
	return 0, nil
}

func TestTLSObservationPublicationChangesAndHeartbeat(t *testing.T) {
	sink := &testTLSObservationSink{}
	publication := tlsObservationPublication{saved: make(map[string]state.TCPListenerTLSObservation)}
	observation := state.TCPListenerTLSObservation{ListenerID: "listener", EdgeID: "edge", Hostname: "echo.example", IntentUpdatedAt: time.Now().Add(-time.Minute), ObservedAt: time.Now()}
	if err := publication.publish(t.Context(), sink, []state.TCPListenerTLSObservation{observation}); err != nil {
		t.Fatal(err)
	}
	observation.ObservedAt = time.Now()
	if err := publication.publish(t.Context(), sink, []state.TCPListenerTLSObservation{observation}); err != nil {
		t.Fatal(err)
	}
	if len(sink.writes) != 1 || len(sink.prunes) != 1 {
		t.Fatalf("unchanged evidence caused excess writes: writes=%d prunes=%d", len(sink.writes), len(sink.prunes))
	}
	observation.Ready, observation.NotAfter = true, time.Now().Add(time.Hour)
	observation.ObservedAt = time.Now()
	if err := publication.publish(t.Context(), sink, []state.TCPListenerTLSObservation{observation}); err != nil {
		t.Fatal(err)
	}
	if len(sink.writes) != 2 || !sink.writes[1].Ready {
		t.Fatal("readiness change was not published immediately")
	}
	old := publication.saved[observation.ListenerID]
	old.ObservedAt = time.Now().Add(-api.TCPListenerTLSObservationRefreshInterval)
	publication.saved[observation.ListenerID] = old
	observation.ObservedAt = time.Now()
	if err := publication.publish(t.Context(), sink, []state.TCPListenerTLSObservation{observation}); err != nil || len(sink.writes) != 3 {
		t.Fatalf("heartbeat missing: writes=%d err=%v", len(sink.writes), err)
	}
	if err := publication.publish(t.Context(), sink, nil); err != nil || len(publication.saved) != 0 {
		t.Fatalf("removed listener retained local observation cache: %v", err)
	}
}

func TestTLSObservationPublicationCancellationAndRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	sink := &testTLSObservationSink{put: func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > api.TCPListenerTLSObservationWriteTimeout {
			return errors.New("publication has no bounded deadline")
		}
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	publication := tlsObservationPublication{saved: make(map[string]state.TCPListenerTLSObservation)}
	observation := state.TCPListenerTLSObservation{ListenerID: "listener", ObservedAt: time.Now()}
	done := make(chan error, 1)
	go func() { done <- publication.publish(ctx, sink, []state.TCPListenerTLSObservation{observation}) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("publication did not reach sink")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("publication ignored cancellation")
	}
	sink.put = nil
	if err := publication.publish(t.Context(), sink, []state.TCPListenerTLSObservation{observation}); err != nil || len(sink.writes) != 1 {
		t.Fatalf("failed write was treated as published: err=%v writes=%d", err, len(sink.writes))
	}
}
