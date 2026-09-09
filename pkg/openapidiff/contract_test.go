package openapidiff

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type contractTestStore struct {
	*state.MemStore
	testAppID string
}

func newTestSnapshotStore(t *testing.T) *contractTestStore {
	t.Helper()
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "contract@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "contract"})
	if err != nil {
		t.Fatal(err)
	}
	return &contractTestStore{MemStore: store, testAppID: app.ID}
}

func TestCompareSnapshotsReportsBreaksAndAdditions(t *testing.T) {
	baseline, err := LoadBytes([]byte(`
openapi: 3.1.0
paths:
  /v1/users:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: string}
                required: [id]
`))
	if err != nil {
		t.Fatal(err)
	}
	proposed, err := LoadBytes([]byte(`
openapi: 3.1.0
paths:
  /v1/users:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: integer}
                  name: {type: string}
                required: [id]
`))
	if err != nil {
		t.Fatal(err)
	}
	baselineRaw, _, err := MarshalSnapshot(baseline)
	if err != nil {
		t.Fatal(err)
	}
	proposedRaw, _, err := MarshalSnapshot(proposed)
	if err != nil {
		t.Fatal(err)
	}

	diff, err := CompareSnapshots(baselineRaw, proposedRaw)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Breaks) != 1 || diff.Breaks[0].Kind != SchemaKindTypeChange {
		t.Fatalf("breaks = %+v, want one type change", diff.Breaks)
	}
	if len(diff.Additions) != 1 || diff.Additions[0].Field != "properties.name" {
		t.Fatalf("additions = %+v, want properties.name addition", diff.Additions)
	}
	if diff.BaselineSHA256 == "" || diff.ProposedSHA256 == "" || diff.BaselineSHA256 == diff.ProposedSHA256 {
		t.Fatalf("hashes = %q/%q, want distinct non-empty hashes", diff.BaselineSHA256, diff.ProposedSHA256)
	}
	message := (&GateError{Diff: diff}).Error()
	if !strings.Contains(message, "type_change") || !strings.Contains(message, "get /v1/users 200") {
		t.Fatalf("gate error = %q, want stable break anchor", message)
	}
}

func TestCompareSnapshotsRejectsInvalidEnvelope(t *testing.T) {
	_, err := CompareSnapshots([]byte(`{}`), []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("CompareSnapshots error = %v, want schema_version validation", err)
	}
}

func TestCheckPromotionAllowsFirstSnapshot(t *testing.T) {
	store := newTestSnapshotStore(t)
	check, err := CheckPromotion(t.Context(), store, store.testAppID, "pending", "prod")
	if !errors.Is(err, ErrSnapshotBaselineMissing) {
		t.Fatalf("CheckPromotion error = %v, want ErrSnapshotBaselineMissing", err)
	}
	if check.HasBaseline || check.Proposed.SHA256 == "" || check.Diff.ProposedSHA256 == "" {
		t.Fatalf("first promotion check = %+v, want proposed hash without baseline", check)
	}
}
