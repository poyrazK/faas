package cosign

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
)

// SignDigest signs the digest its caller computed while publishing; the
// signature verifies against the stored bytes exactly when that digest is
// theirs.
func TestLocalSignerSignDigest(t *testing.T) {
	ctx := context.Background()
	privPath, _ := keyPairTempDir(t)
	pubPath := filepath.Join(filepath.Dir(privPath), "sign-pub.pem")
	const layerKey = "apps/test/00000000-0000-0000-0000-000000000000.ext4"
	sigKey := SigKeyFor(layerKey)
	artifact := bytes.Repeat([]byte("layer "), 1000)
	sum := sha256.Sum256(artifact)
	other := sha256.Sum256([]byte("other bytes"))

	cases := []struct {
		name       string
		digest     string
		wantErr    bool
		wantVerify bool
	}{
		{name: "digest of the stored bytes", digest: hex.EncodeToString(sum[:]), wantVerify: true},
		{name: "digest of other bytes", digest: hex.EncodeToString(other[:]), wantVerify: false},
		{name: "not hex", digest: "zz" + hex.EncodeToString(sum[1:]), wantErr: true},
		{name: "short digest", digest: hex.EncodeToString(sum[:16]), wantErr: true},
		{name: "empty digest", digest: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stor := newMemStorage()
			if err := stor.Put(ctx, layerKey, bytes.NewReader(artifact)); err != nil {
				t.Fatal(err)
			}
			signer, err := NewLocalSigner(privPath, stor, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = signer.SignDigest(ctx, layerKey, sigKey, tc.digest, int64(len(artifact)))
			if tc.wantErr {
				if err == nil {
					t.Fatal("SignDigest accepted a malformed digest")
				}
				if _, written := stor.blobs[sigKey]; written {
					t.Fatal("a refused SignDigest wrote a signature")
				}
				return
			}
			if err != nil {
				t.Fatalf("SignDigest: %v", err)
			}
			verifier, err := NewLocalVerifier(pubPath, stor)
			if err != nil {
				t.Fatal(err)
			}
			if err := verifier.Verify(ctx, layerKey, sigKey); (err == nil) != tc.wantVerify {
				t.Fatalf("Verify = %v, want verified=%v", err, tc.wantVerify)
			}
		})
	}
}
