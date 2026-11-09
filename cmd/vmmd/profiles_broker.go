package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/prometheus/client_golang/prometheus"
)

// profileBrokerResults are the bounded outcomes of one guest profile upload.
var profileBrokerResults = []string{"accepted", "limited", "busy", "read", "protocol", "identity", "forward"}

// profileBroker forwards guest profile uploads to profiled with host-owned
// identity (ADR-819, ADR-1155). The guest is untrusted: any process in it can
// open the vsock port. So the broker reads a connection's frame before it
// takes one of the node's upload slots, admits one connection at a time and
// a bounded number per minute per instance before any database read, and
// reuses an instance's database-derived identity within a VM generation.
type profileBroker struct {
	clock    func() time.Time
	identity func(instance string) (generation string, err error)
	resolve  func(ctx context.Context, instance string) (profiling.Principal, error)
	send     func(ctx context.Context, e profiling.Envelope) error
	slots    chan struct{}
	uploads  *prometheus.CounterVec

	mu        sync.Mutex
	instances map[string]*profileBrokerInstance
}

type profileBrokerInstance struct {
	inFlight  int
	minute    int64
	count     int
	touched   time.Time
	principal *profileBrokerPrincipal
}

type profileBrokerPrincipal struct {
	generation string
	resolved   time.Time
	principal  profiling.Principal
	err        error
}

func newProfileBroker(reg prometheus.Registerer) *profileBroker {
	b := &profileBroker{clock: time.Now, slots: make(chan struct{}, api.ProfileMaxConcurrentUploads), instances: map[string]*profileBrokerInstance{},
		uploads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vmmd_profile_broker_uploads_total",
			Help: "Guest profile uploads by outcome at vmmd's broker, before profiled.",
		}, []string{"result"})}
	for _, result := range profileBrokerResults {
		b.uploads.WithLabelValues(result)
	}
	if reg != nil {
		reg.MustRegister(b.uploads)
	}
	return b
}

// handle serves one guest connection; the caller bounds it with a deadline
// and writes the returned ack byte.
func (b *profileBroker) handle(ctx context.Context, instance string, conn net.Conn) (ack byte, result string, err error) {
	defer func() { b.uploads.WithLabelValues(result).Inc() }()
	release, ok := b.admit(instance)
	if !ok {
		return 1, "limited", fmt.Errorf("profile uploads from this instance are limited")
	}
	defer release()
	body, result, err := readProfileFrame(conn)
	if err != nil {
		return 1, result, err
	}
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		return 1, "busy", fmt.Errorf("profile broker busy")
	}
	principal, err := b.principal(ctx, instance)
	if err != nil {
		return 1, "identity", err
	}
	var upload profiling.Upload
	if err := json.Unmarshal(body, &upload); err != nil {
		return 1, "protocol", fmt.Errorf("invalid profile upload")
	}
	if len(upload.Profile) > api.ProfileMaxCompressedBytes {
		return 1, "protocol", fmt.Errorf("profile payload exceeds bounds")
	}
	if err := b.send(ctx, profiling.Envelope{Principal: principal, Upload: upload}); err != nil {
		return 1, "forward", err
	}
	return 0, "accepted", nil
}

func readProfileFrame(conn net.Conn) ([]byte, string, error) {
	var length [4]byte
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		return nil, "read", err
	}
	n := binary.BigEndian.Uint32(length[:])
	if n == 0 || n > api.ProfileMaxFrameBytes {
		return nil, "protocol", fmt.Errorf("profile frame exceeds bounds")
	}
	body := make([]byte, int(n))
	if _, err := io.ReadFull(conn, body); err != nil {
		return nil, "read", err
	}
	return body, "", nil
}

// admit reserves the instance's connection and counts it in its minute.
func (b *profileBroker) admit(instance string) (release func(), ok bool) {
	now := b.clock()
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.instances[instance]
	if st == nil {
		if len(b.instances) >= api.ProfileBrokerMaxTrackedInstances {
			b.pruneLocked(now)
		}
		if len(b.instances) >= api.ProfileBrokerMaxTrackedInstances {
			return nil, false
		}
		st = &profileBrokerInstance{}
		b.instances[instance] = st
	}
	minute := now.Unix() / 60
	if st.minute != minute {
		st.minute, st.count = minute, 0
	}
	if st.inFlight >= api.ProfileBrokerMaxInFlightPerInstance || st.count >= api.ProfileBrokerUploadsPerInstanceMinute {
		return nil, false
	}
	st.inFlight++
	st.count++
	st.touched = now
	return func() {
		b.mu.Lock()
		st.inFlight--
		b.mu.Unlock()
	}, true
}

// pruneLocked forgets idle instances that have not uploaded for two minutes.
func (b *profileBroker) pruneLocked(now time.Time) {
	for id, st := range b.instances {
		if st.inFlight == 0 && now.Sub(st.touched) > 2*time.Minute {
			delete(b.instances, id)
		}
	}
}

// principal resolves the instance's identity. The in-memory part (is the
// instance serving, which generation) is read every time; the database part
// is reused for ProfileBrokerPrincipalTTL within one generation, refusals
// included, so a guest cannot turn uploads into database reads.
func (b *profileBroker) principal(ctx context.Context, instance string) (profiling.Principal, error) {
	generation, err := b.identity(instance)
	if err != nil {
		return profiling.Principal{}, err
	}
	now := b.clock()
	b.mu.Lock()
	st := b.instances[instance]
	if st != nil && st.principal != nil && st.principal.generation == generation && now.Sub(st.principal.resolved) < api.ProfileBrokerPrincipalTTL {
		cached := *st.principal
		b.mu.Unlock()
		return cached.principal, cached.err
	}
	b.mu.Unlock()
	principal, err := b.resolve(ctx, instance)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return principal, err
	}
	b.mu.Lock()
	if st != nil {
		st.principal = &profileBrokerPrincipal{generation: generation, resolved: now, principal: principal, err: err}
	}
	b.mu.Unlock()
	return principal, err
}
