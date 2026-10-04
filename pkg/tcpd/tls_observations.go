package tcpd

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TLSObservationPublisher can publish edge evidence, never customer intent.
type TLSObservationPublisher interface {
	PutTCPListenerTLSObservation(context.Context, state.TCPListenerTLSObservation) error
	PruneTCPListenerTLSObservations(context.Context, time.Time) (int64, error)
}

type tlsObservationPublication struct {
	saved     map[string]state.TCPListenerTLSObservation
	lastPrune time.Time
}

func observeTLSCertificateExpiry(ctx context.Context, provider CertificateProvider, hostname string) time.Time {
	if provider == nil {
		return time.Time{}
	}
	ctx, cancel := context.WithTimeout(ctx, api.TCPListenerTLSHandshakeTimeout)
	defer cancel()
	certificate, err := provider.Certificate(ctx, hostname)
	if err != nil || ctx.Err() != nil {
		return time.Time{}
	}
	expiry, err := inspectTLSCertificate(certificate, hostname, time.Now())
	if err != nil || ctx.Err() != nil {
		return time.Time{}
	}
	return expiry
}

func (p *tlsObservationPublication) publish(ctx context.Context, sink TLSObservationPublisher, observations []state.TCPListenerTLSObservation) error {
	if sink == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, api.TCPListenerTLSObservationWriteTimeout)
	defer cancel()
	now := time.Now()
	next := make(map[string]state.TCPListenerTLSObservation, len(observations))
	var failure error
	for _, observation := range observations {
		prior, exists := p.saved[observation.ListenerID]
		if exists {
			next[observation.ListenerID] = prior
		}
		if exists && sameTLSObservationEvidence(prior, observation) && now.Sub(prior.ObservedAt) < api.TCPListenerTLSObservationRefreshInterval {
			continue
		}
		if ctx.Err() != nil {
			failure = ctx.Err()
			break
		}
		if err := sink.PutTCPListenerTLSObservation(ctx, observation); err != nil {
			if !errors.Is(err, state.ErrConflict) && failure == nil {
				failure = err
			}
			continue
		}
		next[observation.ListenerID] = observation
	}
	p.saved = next
	if now.Sub(p.lastPrune) >= api.TCPListenerTLSObservationRefreshInterval && ctx.Err() == nil {
		if _, err := sink.PruneTCPListenerTLSObservations(ctx, now.Add(-api.TCPListenerTLSObservationMaxAge)); err != nil {
			if failure == nil {
				failure = err
			}
		} else {
			p.lastPrune = now
		}
	}
	return failure
}

func sameTLSObservationEvidence(a, b state.TCPListenerTLSObservation) bool {
	return a.ListenerID == b.ListenerID && a.EdgeID == b.EdgeID && a.Hostname == b.Hostname && a.IntentUpdatedAt.Equal(b.IntentUpdatedAt) && a.Ready == b.Ready && a.NotAfter.Equal(b.NotAfter)
}
