package main

import (
	"context"
	"crypto/tls"

	"google.golang.org/grpc"

	"github.com/onebox-faas/faas/pkg/overlay"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// computeNodeReader is the one store method the vmmd dial binding reads.
type computeNodeReader interface {
	ComputeNodeByID(ctx context.Context, id string) (state.ComputeNode, error)
}

// computeNodeName resolves a compute_nodes.id to the name its vmmd leaf
// must carry (ADR-20261108124106).
func computeNodeName(store computeNodeReader) func(context.Context, string) (string, bool) {
	return func(ctx context.Context, nodeID string) (string, bool) {
		node, err := store.ComputeNodeByID(ctx, nodeID)
		if err != nil || node.Name == "" {
			return "", false
		}
		return node.Name, true
	}
}

// dialBoundVMMD dials vmmd at target, accepting only the leaf of the named
// node (ADR-20261108124106).
func dialBoundVMMD(vmmdTLS *tls.Config) func(context.Context, string, string) (*grpc.ClientConn, error) {
	return func(ctx context.Context, target, name string) (*grpc.ClientConn, error) {
		return overlay.Dial(ctx, overlay.New(target), wire.ExpectPeerCN(vmmdTLS, name))
	}
}
