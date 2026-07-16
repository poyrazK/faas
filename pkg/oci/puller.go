// Package oci — OCI digest puller (spec §4.6, §9).
//
// The Puller interface is the single seam imaged uses to fetch a digest-pinned
// image. The DefaultPuller shells out to a registry client (skopeo or
// buildctl's own OCI pull); tests substitute a fake that hands back a known
// digest without touching the network. Keeping the seam narrow means M6
// can swap the implementation without touching imaged's orchestration.
package oci

import "context"

// Puller fetches an OCI layer by digest and returns the resolved digest (which
// may differ from the input when the registry canonicalises).
type Puller interface {
	PullDigest(ctx context.Context, ref string) (string, error)
}

// DefaultPuller is the production stub — it just echoes the digest. M5 doesn't
// drive the real registry path (M6 brings it online); this lets imaged walk
// the deployment through every transition without a network dep.
type DefaultPuller struct{}

func (DefaultPuller) PullDigest(_ context.Context, ref string) (string, error) {
	return ref, nil
}
