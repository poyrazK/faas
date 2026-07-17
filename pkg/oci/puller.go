// Package oci — OCI digest puller (spec §4.6, §9).
//
// The Puller interface is the single seam imaged uses to resolve a digest-pinned
// image. RegistryClient (registry.go) is the production implementation: a
// registry v2 client that resolves a reference to its content digest over the
// public registry API (gap G1). DefaultPuller is the offline/test default that
// echoes the reference, so pkg/imaged's orchestration tests need no network.
package oci

import "context"

// Puller fetches an OCI layer by digest and returns the resolved digest (which
// may differ from the input when the registry canonicalises).
type Puller interface {
	PullDigest(ctx context.Context, ref string) (string, error)
}

// DefaultPuller echoes the reference back without touching the network. It is
// the offline default (imaged.New substitutes it when no puller is injected) and
// the shape pkg/imaged tests exercise; production wires oci.RegistryClient.
type DefaultPuller struct{}

func (DefaultPuller) PullDigest(_ context.Context, ref string) (string, error) {
	return ref, nil
}
