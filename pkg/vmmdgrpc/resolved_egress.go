package vmmdgrpc

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// maxResolvedEgressAddresses bounds one AllowResolvedEgress call; a DNS
// answer rarely carries more than a few dozen address records.
const maxResolvedEgressAddresses = 256

type resolvedEgressAllower interface {
	AllowResolvedEgress(ctx context.Context, source netip.Addr, owner fcvm.ServiceCallerIdentity, addrs []netip.Addr, ttl time.Duration, databasePort uint16) error
}

// namedResolvedEgressAllower also checks the answered name, which a
// registries-mode sandbox session's VM needs (ADR-20261027090013 §6).
// *fcvm.Manager implements it.
type namedResolvedEgressAllower interface {
	AllowResolvedEgressNamed(ctx context.Context, source netip.Addr, owner fcvm.ServiceCallerIdentity, name string, addrs []netip.Addr, ttl time.Duration, databasePort uint16) error
}

// AllowResolvedEgress implements the ADR-373 DNS-gated egress hook: the
// node's bridge resolver reports a guest's answer and vmmd adds the
// addresses to that instance's egress_resolved set. A database_port names a
// bound managed database's port (ADR 20261026150000); vmmd opens it to the
// addresses unless ADR-361 forbids the port outright.
func (s *Server) AllowResolvedEgress(ctx context.Context, req *vmmdpb.AllowResolvedEgressRequest) (*vmmdpb.AllowResolvedEgressAck, error) {
	allower, ok := s.vmm.(resolvedEgressAllower)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "vmmd: AllowResolvedEgress not supported by this manager")
	}
	if req.GetInstanceId() == "" || req.GetNetworkGeneration() == "" {
		return nil, status.Error(codes.InvalidArgument, "vmmd: resolved egress requires the original network owner")
	}
	source, err := netip.ParseAddr(req.GetSourceIp())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "vmmd: AllowResolvedEgress: source_ip: %v", err)
	}
	if len(req.GetAddresses()) > maxResolvedEgressAddresses {
		return nil, status.Errorf(codes.InvalidArgument, "vmmd: AllowResolvedEgress: %d addresses exceeds %d", len(req.GetAddresses()), maxResolvedEgressAddresses)
	}
	addrs := make([]netip.Addr, 0, len(req.GetAddresses()))
	for _, raw := range req.GetAddresses() {
		a, err := netip.ParseAddr(raw)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "vmmd: AllowResolvedEgress: address %q: %v", raw, err)
		}
		addrs = append(addrs, a)
	}
	ttl := time.Duration(req.GetTtlSeconds()) * time.Second
	databasePort := req.GetDatabasePort()
	if databasePort > 65535 {
		return nil, status.Errorf(codes.InvalidArgument, "vmmd: AllowResolvedEgress: database_port %d out of range", databasePort)
	}
	if _, forbidden := api.TenantEgressForbiddenPort(int(databasePort)); databasePort != 0 && forbidden {
		return nil, status.Errorf(codes.InvalidArgument, "vmmd: AllowResolvedEgress: database_port %d is never opened", databasePort)
	}
	owner := fcvm.ServiceCallerIdentity{InstanceID: req.GetInstanceId(), NetworkGeneration: req.GetNetworkGeneration()}
	allow := allower.AllowResolvedEgress
	if named, ok := s.vmm.(namedResolvedEgressAllower); ok {
		allow = func(ctx context.Context, source netip.Addr, owner fcvm.ServiceCallerIdentity, addrs []netip.Addr, ttl time.Duration, port uint16) error {
			return named.AllowResolvedEgressNamed(ctx, source, owner, req.GetName(), addrs, ttl, port)
		}
	}
	if err := allow(ctx, source, owner, addrs, ttl, uint16(databasePort)); err != nil {
		if errors.Is(err, fcvm.ErrResolvedEgressNoInstance) {
			// A refused grant leaves the guest's connections to the answer
			// dropped, so name the check that refused it.
			reason := fcvm.ResolvedEgressRefusal(err)
			if s.resolvedEgressRefusals.admit(owner.InstanceID+"\x00"+reason, time.Now()) {
				s.log.Warn("vmmd: DNS answer not granted to its guest", "instance", owner.InstanceID, "source", source.String(), "addresses", len(addrs), "reason", reason)
			}
			return &vmmdpb.AllowResolvedEgressAck{Matched: false}, nil
		}
		return nil, status.Errorf(codes.Internal, "vmmd: AllowResolvedEgress: %v", err)
	}
	return &vmmdpb.AllowResolvedEgressAck{Matched: true}, nil
}

// refusalLogInterval is how often one instance's refused DNS grants are
// logged for each reason: a guest retries a refused lookup many times.
const refusalLogInterval = time.Minute

// refusalLogKeys bounds the keys refusalLog remembers.
const refusalLogKeys = 4096

// refusalLog admits one log line per key per refusalLogInterval. The zero
// value is ready to use.
type refusalLog struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (l *refusalLog) admit(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if last, ok := l.last[key]; ok && now.Sub(last) < refusalLogInterval {
		return false
	}
	if len(l.last) >= refusalLogKeys {
		for k, last := range l.last {
			if now.Sub(last) >= refusalLogInterval {
				delete(l.last, k)
			}
		}
		if len(l.last) >= refusalLogKeys {
			clear(l.last)
		}
	}
	if l.last == nil {
		l.last = make(map[string]time.Time)
	}
	l.last[key] = now
	return true
}
