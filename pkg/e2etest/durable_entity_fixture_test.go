package e2etest

// adr: 678

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func TestDurableEntityS3FixtureConditionalOwnership(t *testing.T) {
	f := NewDurableEntityS3Fixture(t)
	env := map[string]string{}
	for _, entry := range f.APIDEnv(t, "app-id") {
		key, value, _ := strings.Cut(entry, "=")
		env[key] = value
	}
	registry, err := objectstorage.Load(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Resolve(env["FAAS_DURABLE_ENTITY_BACKEND"], env["FAAS_DURABLE_ENTITY_BACKEND_FINGERPRINT"])
	if err != nil {
		t.Fatal(err)
	}
	provider, ok := backend.Provider.(objectstorage.ConditionalStateProvider)
	if !ok {
		t.Fatal("fixture lacks conditional state capability")
	}
	store, err := durableentity.NewProviderStore(provider, env["FAAS_DURABLE_ENTITY_BUCKET"])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := durableentity.Open(t.Context(), store, durableentity.Options{}); err != nil {
		t.Fatal(err)
	}
	old, err := store.Put(t.Context(), "ownership", []byte("first"), "")
	if err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int64
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			_, err := store.Put(t.Context(), "ownership", []byte("successor"), old)
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, durableentity.ErrConflict) {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if successes.Load() != 1 {
		t.Fatalf("CAS successors=%d, want exactly one", successes.Load())
	}
	body, etag, err := store.Get(t.Context(), "ownership", 128)
	if err != nil || string(body) != "successor" || etag == "" || etag == old {
		t.Fatalf("successor read: %s %s %v", body, etag, err)
	}
	if _, err := store.Put(t.Context(), "ownership", []byte("overwritten"), ""); !errors.Is(err, durableentity.ErrConflict) {
		t.Fatalf("duplicate create: %v", err)
	}
}

func TestDurableEntityS3FixtureRejectsUnconditionalAndWrongBucket(t *testing.T) {
	f := NewDurableEntityS3Fixture(t)
	for _, tc := range []struct {
		path, authorization string
		status              int
	}{
		{"/entity-fixture/state", "", http.StatusForbidden},
		{"/other-bucket/state", "AWS4-HMAC-SHA256 fixture", http.StatusForbidden},
		{"/entity-fixture/state", "AWS4-HMAC-SHA256 fixture", http.StatusBadRequest},
	} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, f.server.URL+tc.path, strings.NewReader("state"))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", tc.authorization)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("fixture status=%d, want %d", response.StatusCode, tc.status)
		}
	}
}

func TestHarnessSetAPIDEnvIsPrivateAndValidatesEntries(t *testing.T) {
	h := &Harness{T: t, apidEnv: []string{"PRIVATE=old", "PRIVATE=duplicate", "OTHER=preserved"}, scheddEnv: []string{"PRIVATE=schedd"}}
	if err := h.SetAPIDEnv("PRIVATE", "bucket"); err != nil {
		t.Fatal(err)
	}
	if err := h.SetAPIDEnv("NEW", "value=with-equals"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(h.apidEnv, ",") != "PRIVATE=bucket,PRIVATE=bucket,OTHER=preserved,NEW=value=with-equals" || h.scheddEnv[0] != "PRIVATE=schedd" {
		t.Fatal("apid configuration update changed other daemon settings or lost an entry")
	}
	for _, key := range []string{"", "BAD=KEY", "BAD\x00KEY"} {
		if err := h.SetAPIDEnv(key, "value"); err == nil {
			t.Fatal("accepted malformed environment key")
		}
	}
	if err := h.SetAPIDEnv("KEY", "bad\x00value"); err == nil {
		t.Fatal("accepted malformed environment value")
	}
	var absent *Harness
	if err := absent.SetAPIDEnv("KEY", "value"); err == nil {
		t.Fatal("accepted absent harness")
	}
}
