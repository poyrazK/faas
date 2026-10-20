package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type queueHistoryClient struct {
	history bool
	slug    string
	row     api.QueueBindingResponse
}

func (f *queueHistoryClient) ListQueueBindings(_ context.Context, slug string) ([]api.QueueBindingResponse, error) {
	f.slug = slug
	return nil, nil
}
func (f *queueHistoryClient) ListQueueBindingHistory(_ context.Context, slug string) ([]api.QueueBindingResponse, error) {
	f.slug, f.history = slug, true
	return []api.QueueBindingResponse{f.row}, nil
}

func TestQueueBindingListReviewedRecoveryHistory(t *testing.T) {
	oldOut, oldJSON := (*main.OsStdout), (*main.JsonOutput)
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = oldOut, oldJSON })
	retired := time.Date(2026, 10, 1, 22, 50, 12, 0, time.UTC)
	for _, jsonMode := range []bool{false, true} {
		(*main.JsonOutput) = jsonMode
		var out bytes.Buffer
		(*main.OsStdout) = &out
		f := &queueHistoryClient{row: api.QueueBindingResponse{ID: "11111111222243338444555555555555", Name: "orders", Environment: "production", RetiredAt: &retired}}
		if code := main.CmdQueueBindingList(f, []string{"worker", "--include-retired"}); code != 0 || !f.history || f.slug != "worker" {
			t.Fatalf("history request: %+v %d", f, code)
		}
		if jsonMode {
			var rows []api.QueueBindingResponse
			if err := json.Unmarshal(out.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0].ID != f.row.ID || rows[0].RetiredAt == nil || !rows[0].RetiredAt.Equal(retired) {
				t.Fatalf("history JSON: %s %v", out.String(), err)
			}
		} else if !strings.Contains(out.String(), f.row.ID) || !strings.Contains(out.String(), "retired=2026-10-01T22:50:12Z") {
			t.Fatalf("recovery identity missing: %s", out.String())
		}
	}
	(*main.JsonOutput) = false
	f := &queueHistoryClient{}
	if code := main.CmdQueueBindingList(f, []string{"worker"}); code != 0 || f.history {
		t.Fatalf("default list included retired bindings: %+v %d", f, code)
	}
}
