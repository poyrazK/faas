package fcvm

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/sealsig"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// adr: 20261028161502 — vmmd verifies apid's writer signature on a runtime
// secret before it opens it, on every path that opens runtime secrets.

type signatureTable map[string][]byte

func (s signatureTable) PutRuntimeSecretSignature(_ context.Context, digest, sig []byte) error {
	s[string(digest)] = sig
	return nil
}

func (s signatureTable) RuntimeSecretSignatures(_ context.Context, digests [][]byte) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, d := range digests {
		if sig, ok := s[string(d)]; ok {
			out[string(d)] = sig
		}
	}
	return out, nil
}

func TestRuntimeSecretSignaturesGateOpening(t *testing.T) {
	id := newIdentity(t)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, forger, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	table := signatureTable{}
	ctx := context.Background()
	seal := func(key, value string, signer ed25519.PrivateKey) []byte {
		blob, err := secretbox.SealOneBound(id.Recipient(), secretbox.Binding{AccountID: "acct"}, key, value, 0)
		if err != nil {
			t.Fatal(err)
		}
		if signer != nil {
			if err := sealsig.SignRuntime(ctx, table, sealsig.NewSigner(signer), blob); err != nil {
				t.Fatal(err)
			}
		}
		return blob
	}
	signed := SealedEnvEntry{Key: "DB_URL", SourceKey: "DB_URL", Ciphertext: seal("DB_URL", "postgres://x", private)}
	forged := SealedEnvEntry{Key: "FORGED", SourceKey: "FORGED", Ciphertext: seal("FORGED", "evil", forger)}
	unsigned := SealedEnvEntry{Key: "OLD", SourceKey: "OLD", Ciphertext: seal("OLD", "v", nil)}

	for _, tc := range []struct {
		name    string
		require bool
		entries []SealedEnvEntry
		want    error
	}{
		{"signed", true, []SealedEnvEntry{signed}, nil},
		{"forged", false, []SealedEnvEntry{signed, forged}, sealsig.ErrBadSignature},
		{"unsigned before the contract step", false, []SealedEnvEntry{signed, unsigned}, nil},
		{"unsigned after the contract step", true, []SealedEnvEntry{signed, unsigned}, sealsig.ErrUnsigned},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(&fakeRunner{}, &fakeVMM{})
			m.SetHostIdentity(id)
			checker, err := sealsig.NewRuntimeChecker(table, sealsig.NewVerifier([]ed25519.PublicKey{public}, tc.require), slog.Default())
			if err != nil {
				t.Fatal(err)
			}
			m.WithRuntimeSecretSignatures(checker)
			secrets, err := m.UnsealRuntimeSecrets(context.Background(), tc.entries, "acct")
			files, _, wakeErr := m.prepareWakeFiles(context.Background(), WakeRequest{AccountID: "acct", SealedEnvEntries: tc.entries})
			if tc.want == nil {
				if err != nil || wakeErr != nil || secrets["DB_URL"] != "postgres://x" || len(files) == 0 {
					t.Fatalf("UnsealRuntimeSecrets = %v, %v; prepareWakeFiles = %v", secrets, err, wakeErr)
				}
				return
			}
			refused := tc.entries[len(tc.entries)-1].Key
			if !errors.Is(err, tc.want) || len(secrets) != 0 || !strings.Contains(err.Error(), refused) {
				t.Fatalf("UnsealRuntimeSecrets = %v, %v; want %v naming %s and no values", secrets, err, tc.want, refused)
			}
			if !errors.Is(wakeErr, tc.want) || files != nil {
				t.Fatalf("prepareWakeFiles = %s, %v; want %v and no file", files, wakeErr, tc.want)
			}
		})
	}
}
