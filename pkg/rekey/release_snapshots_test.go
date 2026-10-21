package rekey

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type documentStore struct {
	*fakeStore
	documents map[string]state.SecretRekeyDocument
	fail      string
}

func (s *documentStore) ListSecretRekeyDocuments(_ context.Context, kid, after string, limit int) ([]state.SecretRekeyDocument, error) {
	var docs []state.SecretRekeyDocument
	for _, d := range s.documents {
		if d.Cursor() <= after {
			continue
		}
		needed := false
		_, err := state.TransformSecretRekeyDocument(d, func(e state.SecretRekeyEnvelope) (state.SecretRekeyEnvelope, error) {
			needed = needed || e.Kid != kid
			return e, nil
		})
		if err != nil {
			return nil, err
		}
		if needed {
			docs = append(docs, d)
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Cursor() < docs[j].Cursor() })
	if len(docs) > limit {
		docs = docs[:limit]
	}
	return docs, nil
}
func (s *documentStore) CompareAndSwapSecretRekeyDocument(_ context.Context, doc state.SecretRekeyDocument, replacement json.RawMessage) error {
	if s.fail == doc.Cursor() {
		return state.ErrConflict
	}
	current := s.documents[doc.Cursor()]
	if !bytes.Equal(current.Payload, doc.Payload) {
		return state.ErrConflict
	}
	current.Payload = append(json.RawMessage(nil), replacement...)
	s.documents[doc.Cursor()] = current
	return nil
}
func (s *documentStore) SecretEncryptionKeyUsage(context.Context, string) (state.SecretEncryptionKeyUsage, error) {
	return state.SecretEncryptionKeyUsage{}, nil
}
func (s *documentStore) RetireSecretEncryptionKey(context.Context, string) error { return nil }

func TestReleaseSnapshotRekeyRecovery(t *testing.T) {
	for _, mode := range []string{"restart", "failure", "second_rotation"} {
		t.Run(mode, func(t *testing.T) {
			previous, _ := age.GenerateX25519Identity()
			current, _ := age.GenerateX25519Identity()
			cipher, err := secretbox.SealOne(previous.Recipient(), "TOKEN", "private-value", 4096)
			if err != nil {
				t.Fatal(err)
			}
			row := map[string]any{"id": "immutable-version", "app_id": "owner", "key": "TOKEN", "kid": previous.Recipient().String(), "ciphertext": cipher, "value_hash": "unchanged"}
			store := &documentStore{fakeStore: newFakeStore(), documents: map[string]state.SecretRekeyDocument{}}
			for _, kind := range []string{"app", "bundle", "deployment", "capture"} {
				var value any = []any{row}
				if kind == "capture" {
					value = map[string]any{"workloads": []any{map[string]any{"live_secrets": []any{row}, "secret_target": []any{row}, "secret_snapshot": []any{row}, "target": []any{map[string]any{"key": "ciphertext", "value": "ordinary-variable"}}}}, "descriptor": map[string]any{"environment_hash": "public-proof"}}
				}
				raw, _ := json.Marshal(value)
				doc := state.SecretRekeyDocument{Kind: kind, ID: "middle", Payload: raw}
				store.documents[doc.Cursor()] = doc
			}
			newReplayer := func(identity *age.X25519Identity) *Replayer {
				r, err := New(store, []*age.X25519Identity{identity, current, previous}, newTestHMACKey(t), RekeyConfig{BatchSize: 1, RowsPerSecond: 100000})
				if err != nil {
					t.Fatal(err)
				}
				return r
			}
			var progress RekeyProgress
			if mode == "restart" {
				ctx, cancel := context.WithCancel(t.Context())
				err := newReplayer(current).Run(ctx, "", func(p RekeyProgress) {
					progress = p
					if p.Rekeyed == 1 {
						cancel()
					}
				})
				if !errors.Is(err, context.Canceled) {
					t.Fatal("interrupted rekey did not stop")
				}
				// A late old-key copy behind LastID must be visited on restart.
				doc := store.documents["bundle|middle"]
				doc.Kind, doc.ID = "app", "before"
				store.documents[doc.Cursor()] = doc
			} else if mode == "failure" {
				store.fail = "app|middle"
			}
			if err := newReplayer(current).Run(t.Context(), progress.LastID, func(p RekeyProgress) { progress = p }); err != nil {
				t.Fatal(err)
			}
			if mode == "failure" {
				if progress.Failed != 1 || progress.Rekeyed != 3 {
					t.Fatalf("failed document starved later work: %+v", progress)
				}
				store.fail = ""
				if err := newReplayer(current).Run(t.Context(), progress.LastID, func(p RekeyProgress) { progress = p }); err != nil {
					t.Fatal(err)
				}
				if progress.Failed != 0 || progress.Rekeyed != 1 {
					t.Fatal("retry skipped the earlier failed document")
				}
			}
			if mode == "second_rotation" {
				next, _ := age.GenerateX25519Identity()
				if err := newReplayer(next).Run(t.Context(), progress.LastID, func(p RekeyProgress) { progress = p }); err != nil {
					t.Fatal(err)
				}
				current = next
			}
			for _, doc := range store.documents {
				if !bytes.Contains(doc.Payload, []byte("immutable-version")) {
					t.Fatal("rekey changed logical version identity")
				}
				_, err := state.TransformSecretRekeyDocument(doc, func(e state.SecretRekeyEnvelope) (state.SecretRekeyEnvelope, error) {
					values, err := secretbox.Open(current, e.Ciphertext)
					if err != nil || values["TOKEN"] != "private-value" {
						t.Fatal("rekey did not preserve the encrypted value")
					}
					if _, err := secretbox.Open(previous, e.Ciphertext); err == nil {
						t.Fatal("retired identity still opens the envelope")
					}
					return e, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(doc.Payload, []byte("private-value")) {
					t.Fatal("plaintext entered durable maintenance state")
				}
			}
			if err := newReplayer(current).Run(t.Context(), progress.LastID, func(p RekeyProgress) { progress = p }); err != nil {
				t.Fatal(err)
			}
			if progress.Total != 0 {
				t.Fatal("completed migration was not idempotent")
			}
		})
	}
}
