package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

func TestCILogFollowResumesBinaryPagesWithoutDuplicates(t *testing.T) {
	var output bytes.Buffer
	var offsets []int
	calls, waits := 0, 0
	fetch := func(_ context.Context, offset int) (ci.LogPage, error) {
		offsets = append(offsets, offset)
		calls++
		switch calls {
		case 1:
			return ci.LogPage{Offset: offset, NextOffset: offset + 2, Data: []byte{255, 0}}, nil
		case 2:
			return ci.LogPage{}, io.ErrUnexpectedEOF
		case 3:
			return ci.LogPage{Offset: offset, NextOffset: offset}, nil
		default:
			return ci.LogPage{Offset: offset, NextOffset: offset + 1, Data: []byte("x"), Complete: true}, nil
		}
	}
	last, err := followCILogs(t.Context(), 7, 2, fetch, func(page ci.LogPage) error { _, err := output.Write(page.Data); return err }, func(context.Context) error { waits++; return nil })
	if err != nil || !last.Complete || last.NextOffset != 10 || !bytes.Equal(output.Bytes(), []byte{255, 0, 'x'}) || len(offsets) != 4 || offsets[1] != 9 || offsets[2] != 9 || offsets[3] != 9 || waits != 2 {
		t.Fatalf("follow replay: %+v %v offsets=%v waits=%d", last, err, offsets, waits)
	}
}

func TestCILogFollowRefusesInvalidCursorsAndStopsOnCancellation(t *testing.T) {
	for _, page := range []ci.LogPage{
		{Offset: 0, NextOffset: 1, Data: []byte("x")},
		{Offset: 5, NextOffset: 8, Data: []byte("x")},
		{Offset: 5, NextOffset: 8, Data: []byte("big")},
	} {
		emitted := false
		if _, err := followCILogs(t.Context(), 5, 2, func(context.Context, int) (ci.LogPage, error) { return page, nil }, func(ci.LogPage) error { emitted = true; return nil }, func(context.Context) error { return nil }); err == nil || emitted {
			t.Fatal("invalid page emitted bytes")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	last, err := followCILogs(ctx, api.CILogMaxBytesPerAttempt, 2, func(context.Context, int) (ci.LogPage, error) {
		return ci.LogPage{Offset: api.CILogMaxBytesPerAttempt, NextOffset: api.CILogMaxBytesPerAttempt, Truncated: true}, nil
	}, func(ci.LogPage) error { return nil }, func(context.Context) error { cancel(); return ctx.Err() })
	if !errors.Is(err, context.Canceled) || last.Complete || !last.Truncated || last.NextOffset != api.CILogMaxBytesPerAttempt {
		t.Fatal("truncation claimed native completion or lost cursor")
	}
}

func TestCILogFollowRetryBudgetAndAuthorization(t *testing.T) {
	for _, status := range []int{401, 403, 404, 410, 429, 503} {
		calls := 0
		_, err := followCILogs(t.Context(), 4, 2, func(context.Context, int) (ci.LogPage, error) {
			calls++
			return ci.LogPage{}, &operatorHTTPError{Status: status}
		}, func(ci.LogPage) error { t.Error("error response emitted output"); return nil }, func(context.Context) error { return nil })
		want := 1
		if status == 429 || status == 503 {
			want = api.CILogFollowMaxRetries + 1
		}
		if err == nil || calls != want {
			t.Fatalf("status %d calls=%d", status, calls)
		}
	}
	o := ciExecutionOptions{id: uuid.NewString(), attempt: uuid.NewString(), follow: true, offset: 4, limit: 2}
	if _, path, _, _, err := ciExecutionRequest("logs", o); err != nil || !strings.HasSuffix(path, "?offset=4&limit=2") {
		t.Fatal("follow lost its resume cursor")
	}
	for _, command := range []string{"submit", "status", "rerun", "artifact", "list"} {
		if _, _, _, _, err := ciExecutionRequest(command, o); err == nil {
			t.Fatal("follow flag accepted outside logs")
		}
	}
}
