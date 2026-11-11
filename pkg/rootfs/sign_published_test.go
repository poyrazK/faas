package rootfs

import (
	"context"
	"testing"
)

// digestRecordingSigner records which path signPublished took.
type digestRecordingSigner struct {
	signs, digests int
	digestHex      string
	bytes          int64
}

func (s *digestRecordingSigner) Sign(context.Context, string, string) error {
	s.signs++
	return nil
}

func (s *digestRecordingSigner) SignDigest(_ context.Context, _, _ string, digestHex string, bytes int64) error {
	s.digests++
	s.digestHex, s.bytes = digestHex, bytes
	return nil
}

func TestSignPublishedSignsThePublishedDigest(t *testing.T) {
	const hexDigest = "4f2cd292b01dbf88e8de5b6990d3f83a8c8b0465fd2f8e17b48be49ee81daa40"
	cases := []struct {
		name        string
		identity    ArtifactIdentity
		wantDigests int
		wantSigns   int
	}{
		{name: "published identity", identity: ArtifactIdentity{Digest: "sha256:" + hexDigest, Bytes: 1 << 30}, wantDigests: 1},
		{name: "no identity", identity: ArtifactIdentity{}, wantSigns: 1},
		{name: "other algorithm", identity: ArtifactIdentity{Digest: "sha512:" + hexDigest, Bytes: 10}, wantSigns: 1},
		{name: "no length", identity: ArtifactIdentity{Digest: "sha256:" + hexDigest}, wantSigns: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &digestRecordingSigner{}
			b := (&Builder{}).WithSigner(s)
			if err := b.signPublished(context.Background(), "apps/a/x.ext4", tc.identity); err != nil {
				t.Fatal(err)
			}
			if s.digests != tc.wantDigests || s.signs != tc.wantSigns {
				t.Fatalf("SignDigest %d, Sign %d; want %d and %d", s.digests, s.signs, tc.wantDigests, tc.wantSigns)
			}
			if tc.wantDigests == 1 && (s.digestHex != hexDigest || s.bytes != tc.identity.Bytes) {
				t.Fatalf("signed %q (%d bytes), want %q (%d)", s.digestHex, s.bytes, hexDigest, tc.identity.Bytes)
			}
		})
	}

	// A signer without SignDigest reads the layer back as before.
	plain := &fakeSigner{}
	if err := (&Builder{}).WithSigner(plain).signPublished(context.Background(), "apps/a/x.ext4",
		ArtifactIdentity{Digest: "sha256:" + hexDigest, Bytes: 10}); err != nil || plain.signed != 1 {
		t.Fatalf("plain signer: err %v, signed %d", err, plain.signed)
	}
	if err := (&Builder{}).signPublished(context.Background(), "apps/a/x.ext4", ArtifactIdentity{}); err != nil {
		t.Fatalf("nil signer: %v", err)
	}
}
