// adr: 712
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestJournalExceedsLegacyBudgetAndKeepsOriginalResults(t *testing.T) {
	f := newFixture(t)
	const total = api.MaxDurableEntityReceipts + 33
	results := make([]Result, total)
	for i := range total {
		result, err := f.manager.Execute(t.Context(), f.claim, request(fmt.Sprint(i)), func(ctx context.Context, v View) (Transition, error) {
			transition, err := increment(ctx, v)
			transition.Result = json.RawMessage(fmt.Sprintf(`{ "original": %d, "padding": "%s" }`, i, strings.Repeat("x", 1024)))
			return transition, err
		})
		if err != nil {
			t.Fatal(i, err)
		}
		results[i] = result
	}
	value, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := f.store.Get(t.Context(), value.SnapshotKey, api.MaxDurableEntitySnapshotBytes)
	if err != nil || len(body) > 1024 {
		t.Fatalf("unbounded main snapshot: %d %v", len(body), err)
	}
	restarted := openManager(t, f.store, f.clock)
	collectAll(t, restarted, f.claim)
	for i, original := range results {
		replayed, err := restarted.Execute(t.Context(), f.claim, request(fmt.Sprint(i)), func(context.Context, View) (Transition, error) {
			t.Fatal("replay dispatched handler")
			return Transition{}, nil
		})
		if err != nil || !replayed.Replayed || replayed.Version != original.Version || string(replayed.Value) != string(original.Value) {
			t.Fatal(i, replayed, err)
		}
	}
	if _, err := restarted.Execute(t.Context(), f.claim, Request{ID: "0", Payload: json.RawMessage(`{}`)}, increment); !errors.Is(err, ErrRequestConflict) {
		t.Fatal(err)
	}
	assertCount(t, t.Context(), restarted, f.id, total, total)
}

func TestUnpublishedJournalIsNeverReplayAuthority(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("one"), increment); err != nil {
		t.Fatal(err)
	}
	failed := false
	store := wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
		if strings.HasSuffix(key, "/manifest.json") && !failed {
			failed = true
			return "", ErrConflict
		}
		return f.store.Put(ctx, key, body, etag)
	}}
	m := openManager(t, store, f.clock)
	if _, err := m.Execute(t.Context(), f.claim, request("orphan"), increment); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := f.manager.Execute(t.Context(), f.claim, request("other"), increment); err != nil {
		t.Fatal(err)
	}
	collectAll(t, f.manager, f.claim)
	called := false
	result, err := f.manager.Execute(t.Context(), f.claim, request("orphan"), func(ctx context.Context, view View) (Transition, error) { called = true; return increment(ctx, view) })
	if err != nil || !called || result.Replayed || result.Version != 3 {
		t.Fatal(result, called, err)
	}
}

func TestMissingCommittedReceiptFailsClosed(t *testing.T) {
	for _, corruption := range []string{"missing", "hash", "identity", "children"} {
		t.Run(corruption, func(t *testing.T) {
			f := newFixture(t)
			for _, id := range []string{"one", "two"} {
				if _, err := f.manager.Execute(t.Context(), f.claim, request(id), increment); err != nil {
					t.Fatal(err)
				}
			}
			value, etag, err := f.manager.readManifest(t.Context(), f.id)
			if err != nil {
				t.Fatal(err)
			}
			state, err := f.manager.readSnapshot(t.Context(), value)
			if err != nil {
				t.Fatal(err)
			}
			f.store.mu.Lock()
			object := f.store.objects[state.ReceiptRoot.Key]
			switch corruption {
			case "missing":
				delete(f.store.objects, state.ReceiptRoot.Key)
			case "hash":
				object.body = []byte(`{}`)
				f.store.objects[state.ReceiptRoot.Key] = object
			default:
				var node journalNode
				if err := json.Unmarshal(object.body, &node); err != nil {
					t.Fatal(err)
				}
				if corruption == "identity" {
					node.ID.AppID = "other"
				} else {
					node.Children = map[string]journalRef{"x": *state.ReceiptRoot}
				}
				object.body, err = json.Marshal(node)
				if err != nil {
					t.Fatal(err)
				}
				state.ReceiptRoot.Hash = digest(object.body)
				f.store.objects[state.ReceiptRoot.Key] = object
				body, marshalErr := json.Marshal(state)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				saved := f.store.objects[value.SnapshotKey]
				saved.body = body
				f.store.objects[value.SnapshotKey] = saved
				value.SnapshotHash = digest(body)
			}
			f.store.mu.Unlock()
			if err := f.manager.putManifest(t.Context(), value, etag); err != nil {
				t.Fatal(err)
			}
			_, err = f.manager.Execute(t.Context(), f.claim, request("one"), func(context.Context, View) (Transition, error) {
				t.Fatal("missing receipt dispatched handler")
				return Transition{}, nil
			})
			if !errors.Is(err, ErrCorrupt) {
				t.Fatal(err)
			}
		})
	}
}
