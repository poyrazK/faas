package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func TestQuotaWarningCommandsDisabledReadsOnlyOwnFlag(t *testing.T) {
	for _, value := range []string{"", "0", "true", " 1", "1 "} {
		commands, err := configureQuotaWarningCommands(t.Context(), func(k string) string {
			if k != internalmail.QuotaWarningOutboxEnableEnv {
				t.Fatal("disabled meterd read config", k)
			}
			return value
		})
		if err != nil || commands != nil {
			t.Fatal(commands, err)
		}
	}
}

func TestQuotaWarningCommandsPrivateProbeAndRuntimeGates(t *testing.T) {
	token := strings.Repeat("s", 32)
	path := filepath.Join(t.TempDir(), "quota-warning.token")
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	operator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("private credential missing")
		}
		if strings.HasSuffix(r.URL.Path, "/readyz") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(api.InternalMailQuotaWarningResponse{})
	}))
	defer operator.Close()
	flags := map[string]string{internalmail.QuotaWarningOutboxEnableEnv: "1", internalmail.EnableEnv: "1", internalmail.ProducersEnableEnv: "1", internalmail.SchedulingEnableEnv: "1", internalmail.ProducerOutboxEnableEnv: "1", api.InternalMailQuotaWarningTokenPathEnv: path, "FAAS_APID_INTERNAL_BASE_URL": operator.URL}
	getenv := func(k string) string { return flags[k] }
	commands, err := configureQuotaWarningCommands(t.Context(), getenv)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("enabled startup skipped private readiness")
	}
	id := uuid.NewString()
	if _, err := commands.Warn(t.Context(), id, time.Now().UTC().Truncate(24*time.Hour), api.PlanPro); err != nil || calls.Load() != 2 {
		t.Fatal(err)
	}
	flags[internalmail.EnableEnv] = "0"
	if _, err := commands.Warn(t.Context(), id, time.Now().UTC().Truncate(24*time.Hour), api.PlanPro); err == nil || calls.Load() != 2 {
		t.Fatal("parent withdrawal allowed command I/O")
	}
	if err := commands.Complete(t.Context(), id, uuid.NewString(), uuid.NewString()); err == nil || calls.Load() != 2 {
		t.Fatal("parent withdrawal allowed completion I/O")
	}
	if _, err := configureQuotaWarningCommands(t.Context(), getenv); err == nil {
		t.Fatal("missing parent gates accepted")
	}
}
