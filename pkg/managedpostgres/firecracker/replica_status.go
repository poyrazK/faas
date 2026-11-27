package firecracker

import (
	"context"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (p *Provider) ReplicaStatus(ctx context.Context, id string) (*api.ManagedPostgresReplicaStatus, error) {
	out := &api.ManagedPostgresReplicaStatus{}
	reader, ok := p.scheduler.(interface {
		GetDatabaseReplica(context.Context, *scheddpb.DatabaseRef, ...grpc.CallOption) (*scheddpb.DatabaseReplicaStatus, error)
	})
	if !ok {
		return out, nil
	}
	probe, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	replica, err := reader.GetDatabaseReplica(probe, &scheddpb.DatabaseRef{DatabaseId: id})
	switch status.Code(err) {
	case codes.Unimplemented, codes.Unavailable, codes.DeadlineExceeded, codes.NotFound:
		return out, nil
	case codes.OK:
	default:
		return out, providerError(err)
	}
	if observed := replica.GetObservedAt(); observed != nil && observed.IsValid() {
		sampledAt := observed.AsTime()
		age := max(time.Since(sampledAt).Milliseconds(), 0)
		out.ObservedAt, out.SampleAgeMillis = &sampledAt, &age
		out.Available, out.ReplayLagBytes = replica.GetAvailable(), replica.ReplayLagBytes
	}
	return out, nil
}
