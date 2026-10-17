package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

type ciLogFetch func(context.Context, int) (ci.LogPage, error)
type ciLogWait func(context.Context) error

func waitCILogPoll(ctx context.Context) error {
	timer := time.NewTimer(time.Duration(api.CILogFollowPollMillis) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryCILogFetch(err error) bool {
	var problem *operatorHTTPError
	if errors.As(err, &problem) {
		return problem.Status == 429 || problem.Status >= 500 && problem.Status <= 504
	}
	var network net.Error
	return errors.As(err, &network) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}

// Fetch entire pages before emitting bytes. A lost HTTP response therefore
// retries the same cursor and cannot duplicate already emitted output.
func followCILogs(ctx context.Context, offset, limit int, fetch ciLogFetch, emit func(ci.LogPage) error, wait ciLogWait) (ci.LogPage, error) {
	last := ci.LogPage{Offset: offset, NextOffset: offset}
	failures := 0
	for {
		if err := ctx.Err(); err != nil {
			return last, err
		}
		page, err := fetch(ctx, last.NextOffset)
		if err != nil {
			if !retryCILogFetch(err) || failures >= api.CILogFollowMaxRetries {
				return last, err
			}
			failures++
			if err := wait(ctx); err != nil {
				return last, err
			}
			continue
		}
		failures = 0
		if page.Offset != last.NextOffset || page.Offset < 0 || page.Offset > api.CILogMaxBytesPerAttempt || len(page.Data) > limit || page.NextOffset != page.Offset+len(page.Data) || page.NextOffset > api.CILogMaxBytesPerAttempt {
			return last, fmt.Errorf("invalid CI log page or cursor")
		}
		if err := emit(page); err != nil {
			return last, err
		}
		last = page
		if page.Complete {
			return last, nil
		}
		if len(page.Data) < limit {
			if err := wait(ctx); err != nil {
				return last, err
			}
		}
	}
}

func runCILogFollow(client *operatorHTTPClient, o ciExecutionOptions) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fetch := func(ctx context.Context, offset int) (ci.LogPage, error) {
		next := o
		next.offset = offset
		method, path, body, headers, err := ciExecutionRequest("logs", next)
		if err != nil {
			return ci.LogPage{}, err
		}
		var page *ci.LogPage
		if err := client.doJSONWithHeaders(ctx, method, path, body, &page, false, nil, headers); err != nil {
			return ci.LogPage{}, err
		}
		if page == nil {
			return ci.LogPage{}, fmt.Errorf("missing CI log page")
		}
		return *page, nil
	}
	jsonOutput := jsonEnabled()
	emit := func(page ci.LogPage) error {
		if jsonOutput {
			return json.NewEncoder(osStdout).Encode(page)
		}
		_, err := osStdout.Write(page.Data)
		return err
	}
	last, err := followCILogs(ctx, o.offset, o.limit, fetch, emit, waitCILogPoll)
	_, _ = fmt.Fprintf(osStderr, "next_offset=%d complete=%t truncated=%t\n", last.NextOffset, last.Complete, last.Truncated)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 130
		}
		_, _ = fmt.Fprintln(osStderr, "internal CI:", err)
		return 1
	}
	return 0
}
