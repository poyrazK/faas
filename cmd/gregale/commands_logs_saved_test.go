package main_test

// adr: 20261018083536 — `gregale logs saved` stores filters without a window
// and `--saved` sends the name with the search.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdLogsSavedSaveListDelete(t *testing.T) {
	var calls []string
	var putBody api.LogSavedSearchRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &putBody)
			_ = json.NewEncoder(w).Encode(api.LogSavedSearch{Name: "db-errors", Query: putBody.Query, CreatedAt: time.Now(), UpdatedAt: time.Now()})
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			_ = json.NewEncoder(w).Encode(api.LogSavedSearchesResponse{Searches: []api.LogSavedSearch{{Name: "db-errors", Query: "level=error&q=db"}}})
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	stdout := captureLogSearchStdout(t)

	if code := main.CmdLogs([]string{"saved", "save", "db-errors", "shop", "--level", "error", "--q", "db", "--since", "6h"}); code != 0 {
		t.Fatalf("logs saved save = %d", code)
	}
	if putBody.Query != "level=error&q=db" {
		t.Fatalf("saved query = %q, want the filters without the window", putBody.Query)
	}
	if code := main.CmdLogs([]string{"saved", "shop"}); code != 0 {
		t.Fatalf("logs saved = %d", code)
	}
	if code := main.CmdLogs([]string{"saved", "delete", "db-errors", "shop"}); code != 0 {
		t.Fatalf("logs saved delete = %d", code)
	}
	want := []string{"PUT /v1/apps/shop/log-searches/db-errors", "GET /v1/apps/shop/log-searches", "DELETE /v1/apps/shop/log-searches/db-errors"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	out := stdout.String()
	for _, s := range []string{"saved db-errors on shop", "--saved db-errors", "db-errors                level=error&q=db", "deleted saved search db-errors on shop"} {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q\nfull: %s", s, out)
		}
	}
	if code := main.CmdLogs([]string{"saved", "save", "empty", "shop", "--since", "1h"}); code == 0 {
		t.Fatal("saving a search without filters succeeded")
	}
}

func TestCmdLogsSearchSendsSavedName(t *testing.T) {
	_, gotQuery := logSearchServer(t, api.LogEventsResponse{})
	_ = captureLogSearchStdout(t)
	if code := main.CmdLogs([]string{"search", "shop", "--saved", "db-errors", "--since", "1h"}); code != 0 {
		t.Fatalf("logs search = %d", code)
	}
	if gotQuery.Get("saved") != "db-errors" || gotQuery.Get("since") != "1h" {
		t.Fatalf("query = %v, want saved and since", *gotQuery)
	}
}
