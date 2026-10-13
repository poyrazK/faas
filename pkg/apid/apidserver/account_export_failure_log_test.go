// spec: §17

package apidserver

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// failingUsageStore makes the export's usage section fail.
type failingUsageStore struct{ Store state.Store }

func (failingUsageStore) UsageByAccount(context.Context, string, time.Time) ([]state.Usage, error) {
	return nil, errors.New("usage scan canceled")
}

// TestExportAccount_AssemblyFailureIsLogged pins H9-136: on production-us
// `gregale account export` failed with a generic 503 on every attempt and
// apid logged nothing, so the cause could not be found.
func TestExportAccount_AssemblyFailureIsLogged(t *testing.T) {
	withAccountTestRecipient(t)
	e := setup(t, api.PlanHobby)
	seedOneApp(t, e, "exp-fail")
	var logs bytes.Buffer
	e.s.log = slog.New(slog.NewTextHandler(&logs, nil))
	e.s.store = statetest.NewStore(failingUsageStore{Store: e.s.store})

	rec := e.do(t, "GET", "/v1/account/export", nil, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("export: %d %s, want 503", rec.Code, rec.Body)
	}
	got := logs.String()
	for _, want := range []string{"account export assembly failed", e.acct.ID, "load usage: usage scan canceled", "elapsed_ms="} {
		if !strings.Contains(got, want) {
			t.Fatalf("log lacks %q:\n%s", want, got)
		}
	}
}
