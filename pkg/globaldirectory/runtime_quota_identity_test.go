package globaldirectory

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func exerciseQuotaIssuer(t *testing.T, store RuntimeQuotaIssuerStore) {
	t.Helper()
	ids := []string{uuid.NewString(), uuid.NewString()}
	var wg sync.WaitGroup
	results := make(chan string, 2)
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.BindRuntimeQuotaIssuer(t.Context(), id); err == nil {
				results <- id
			} else if !errors.Is(err, ErrConflict) {
				t.Errorf("bind issuer: %v", err)
			}
		}()
	}
	wg.Wait()
	close(results)
	winner := ""
	for id := range results {
		if winner != "" {
			t.Fatal("two directory issuers committed")
		}
		winner = id
	}
	if winner == "" {
		t.Fatal("no issuer committed")
	}
	if err := store.BindRuntimeQuotaIssuer(t.Context(), winner); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", uuid.Nil.String(), uuid.NewString()} {
		if err := store.BindRuntimeQuotaIssuer(t.Context(), bad); !errors.Is(err, ErrConflict) {
			t.Fatalf("replaced issuer: %v", err)
		}
	}
}

func TestRuntimeQuotaIssuerMemory(t *testing.T) { exerciseQuotaIssuer(t, NewMemoryStore()) }

func TestRuntimeQuotaIssuerCannotBeRegisteredCell(t *testing.T) {
	s := NewMemoryStore()
	cell := Cell{ID: uuid.NewString(), Region: "eu-fsn", APIBase: "https://fsn.example.com", Enabled: true}
	if _, err := s.PutCell(t.Context(), cell); err != nil {
		t.Fatal(err)
	}
	if err := s.BindRuntimeQuotaIssuer(t.Context(), cell.ID); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	cell.ID = uuid.NewString()
	if err := s.BindRuntimeQuotaIssuer(t.Context(), cell.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutCell(t.Context(), cell); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
