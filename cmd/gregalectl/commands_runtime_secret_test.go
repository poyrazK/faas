package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/sealsig"
	"github.com/onebox-faas/faas/pkg/state"
)

// runtimeSignDocs is an in-memory runtimeSignStore.
type runtimeSignDocs struct {
	docs []state.SecretBindingDocument
	sigs map[string][]byte
}

func (s *runtimeSignDocs) ListSecretBindingDocuments(_ context.Context, after string, limit int) ([]state.SecretBindingDocument, error) {
	var page []state.SecretBindingDocument
	for _, doc := range s.docs {
		if doc.Cursor() > after && len(page) < limit {
			page = append(page, doc)
		}
	}
	return page, nil
}

func (s *runtimeSignDocs) PutRuntimeSecretSignature(_ context.Context, digest, sig []byte) error {
	s.sigs[string(digest)] = sig
	return nil
}

func (s *runtimeSignDocs) RuntimeSecretSignatures(_ context.Context, digests [][]byte) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, d := range digests {
		if sig, ok := s.sigs[string(d)]; ok {
			out[string(d)] = sig
		}
	}
	return out, nil
}

func runtimeSignDoc(t *testing.T, kind, id string, blobs ...[]byte) state.SecretBindingDocument {
	t.Helper()
	type envelope struct {
		Key        string `json:"key"`
		Kid        string `json:"kid"`
		ValueHash  string `json:"value_hash"`
		Ciphertext []byte `json:"ciphertext"`
	}
	var rows []envelope
	for _, blob := range blobs {
		rows = append(rows, envelope{Key: "K", Kid: "age1kid", ValueHash: "0123456789abcdef", Ciphertext: blob})
	}
	payload, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return state.SecretBindingDocument{SecretRekeyDocument: state.SecretRekeyDocument{Kind: kind, ID: id, Payload: payload}}
}

// adr: 20261028161502 — the backfill signs every stored runtime envelope
// that has no signature, once per distinct envelope, and reports the ones
// signed by another key instead of re-signing them.
func TestSignExistingRuntime(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, other, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	unsigned, signed, foreign := []byte("unsigned"), []byte("signed"), []byte("foreign")
	store := &runtimeSignDocs{sigs: map[string][]byte{}}
	if err := sealsig.SignRuntime(ctx, store, sealsig.NewSigner(private), signed); err != nil {
		t.Fatal(err)
	}
	if err := sealsig.SignRuntime(ctx, store, sealsig.NewSigner(other), foreign); err != nil {
		t.Fatal(err)
	}
	// In cursor order, as the store lists them.
	store.docs = []state.SecretBindingDocument{
		runtimeSignDoc(t, "app", "1", unsigned, signed),
		{SecretRekeyDocument: state.SecretRekeyDocument{Kind: "bundle", ID: "4", Payload: json.RawMessage(`{"not":"an array"}`)}},
		runtimeSignDoc(t, "deployment", "2", unsigned), // a snapshot copy of the same bytes
		runtimeSignDoc(t, "project", "3", foreign),
	}

	report, err := signExistingRuntime(ctx, store, private)
	if err != nil {
		t.Fatal(err)
	}
	want := runtimeSignReport{Envelopes: 3, Signed: 1, AlreadySigned: 1, Invalid: 1, Unparsed: 1}
	if report != want {
		t.Fatalf("report = %+v, want %+v", report, want)
	}
	verifier := sealsig.NewVerifier([]ed25519.PublicKey{public}, true)
	if err := verifier.Check(sealsig.RuntimeSecretMessage(unsigned), store.sigs[string(sealsig.RuntimeDigest(unsigned))]); err != nil {
		t.Fatalf("the backfilled signature = %v", err)
	}
	again, err := signExistingRuntime(ctx, store, private)
	if err != nil || again.Signed != 0 || again.AlreadySigned != 2 {
		t.Fatalf("rerun = %+v, %v; want nothing left to sign", again, err)
	}
}
