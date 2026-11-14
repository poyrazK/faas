// ADR-20261109170000: v2 signatures bind a layer to its storage key.
package cosign

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSignatureV2BindsTheStorageKey(t *testing.T) {
	ctx := context.Background()
	privPath, pubPath := keyPairTempDir(t)
	mem := newMemStorage()
	const keyA = "apps/alice/11111111-1111-1111-1111-111111111111.ext4"
	const keyB = "apps/bob/22222222-2222-2222-2222-222222222222.ext4"
	mem.blobs[keyA] = bytes.Repeat([]byte("alice's code "), 512)
	mem.blobs[keyB] = bytes.Repeat([]byte("bob's code "), 512)

	signer, err := NewLocalSigner(privPath, mem, nil)
	if err != nil {
		t.Fatal(err)
	}
	signer.WithSignatureV2(true)
	for _, k := range []string{keyA, keyB} {
		if err := signer.Sign(ctx, k, SigKeyFor(k)); err != nil {
			t.Fatal(err)
		}
	}
	var doc SignatureV2
	if err := json.Unmarshal(mem.blobs[SigKeyFor(keyA)], &doc); err != nil || doc.Format != SignatureFormatV2 || doc.Key != keyA || len(doc.Signature) != 64 {
		t.Fatalf("stored signature = %s, %v; want a v2 document for %s", mem.blobs[SigKeyFor(keyA)], err, keyA)
	}

	newVerifier := func() *LocalVerifier {
		v, err := NewLocalVerifier(pubPath, mem)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if err := newVerifier().Verify(ctx, keyA, SigKeyFor(keyA)); err != nil {
		t.Fatalf("v2 round trip: %v", err)
	}
	if err := newVerifier().CheckPresent(ctx, keyA, SigKeyFor(keyA)); err != nil {
		t.Fatalf("CheckPresent of a v2 signature: %v", err)
	}

	// The attack: copy Alice's signed layer and signature over Bob's keys.
	mem.blobs[keyB] = append([]byte(nil), mem.blobs[keyA]...)
	mem.blobs[SigKeyFor(keyB)] = append([]byte(nil), mem.blobs[SigKeyFor(keyA)]...)
	var problem *api.Problem
	if err := newVerifier().Verify(ctx, keyB, SigKeyFor(keyB)); !errors.As(err, &problem) || problem.Code != "sig_invalid" {
		t.Fatalf("a layer moved to another key verified: %v", err)
	}
	if err := newVerifier().CheckPresent(ctx, keyB, SigKeyFor(keyB)); !errors.As(err, &problem) || problem.Code != "sig_invalid" {
		t.Fatalf("CheckPresent accepted a signature for another key: %v", err)
	}

	// Tampered bytes under the right key still fail.
	mem.blobs[keyA] = append(mem.blobs[keyA], 'x')
	if err := newVerifier().Verify(ctx, keyA, SigKeyFor(keyA)); !errors.As(err, &problem) || problem.Code != "sig_invalid" {
		t.Fatalf("tampered v2 layer verified: %v", err)
	}
}

// Layers signed before v2 keep booting until the re-sign pass converts them.
func TestVerifierStillAcceptsV1(t *testing.T) {
	ctx := context.Background()
	privPath, pubPath := keyPairTempDir(t)
	mem := newMemStorage()
	const key = "apps/carol/33333333-3333-3333-3333-333333333333.ext4"
	mem.blobs[key] = bytes.Repeat([]byte{7}, 4096)
	signer, err := NewLocalSigner(privPath, mem, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := signer.Sign(ctx, key, SigKeyFor(key)); err != nil {
		t.Fatal(err)
	}
	if n := len(mem.blobs[SigKeyFor(key)]); n != 64 {
		t.Fatalf("default signer wrote %d bytes, want a 64-byte v1 signature", n)
	}
	v, err := NewLocalVerifier(pubPath, mem)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Verify(ctx, key, SigKeyFor(key)); err != nil {
		t.Fatalf("v1 signature rejected: %v", err)
	}
}

func TestCheckSignatureShape(t *testing.T) {
	good, err := json.Marshal(SignatureV2{Format: SignatureFormatV2, Key: "k", Digest: string(bytes.Repeat([]byte("a"), 64)), Signature: make([]byte, 64)})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		blob []byte
		ok   bool
	}{
		{"v1", make([]byte, 64), true},
		{"v2", good, true},
		{"short", make([]byte, 12), false},
		{"other format", []byte(`{"format":"x","key":"k"}`), false},
		{"huge", make([]byte, maxSignatureV2Bytes+1), false},
	} {
		if err := checkSignatureShape("k", tc.blob); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
	if err := checkSignatureShape("other", good); err == nil {
		t.Error("a v2 signature for another key passed the shape check")
	}
}
