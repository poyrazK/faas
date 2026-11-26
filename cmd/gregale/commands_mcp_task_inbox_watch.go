package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

const maxMCPTaskInboxWatchPages = 64

type mcpTaskInboxWatchOptions struct {
	interval, scanTimeout time.Duration
	checkpointPath        string
	once                  bool
}

func watchMCPTaskInbox(ctx context.Context, client *mcphosting.Client, query mcphosting.TaskInboxQuery, options mcpTaskInboxWatchOptions) error {
	var previous map[string]struct{}
	var checkpoint *mcpTaskInboxCheckpoint
	if options.checkpointPath != "" {
		var err error
		checkpoint, err = openMCPTaskInboxCheckpoint(options.checkpointPath, client.Endpoint, query.Status)
		if err != nil {
			return err
		}
		defer checkpoint.close()
		previous = checkpoint.observed
	}
	key := mcpTaskInboxWatchKey(client, query.Status)
	nextRetry := options.interval
	retrying := false
	for {
		scanCtx, cancel := context.WithTimeout(ctx, options.scanTimeout)
		items, exchange, err := scanMCPTaskInbox(scanCtx, client, query)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}

		delay := options.interval
		if err != nil {
			if !mcpTaskMetadataReadRetryable(exchange.HTTPStatus, err) &&
				!isTransportError(err) && !errors.Is(err, io.ErrUnexpectedEOF) {
				return err
			}
			if !retrying {
				PrintWarn(osStderr, "MCP Task inbox scan interrupted. Retrying.")
				retrying = true
			}
			delay = mcpTaskInboxWatchRetryDelay(err, nextRetry)
			nextRetry = min(2*nextRetry, max(options.interval, time.Minute))
		} else {
			var emitted bool
			previous, emitted, err = publishMCPTaskInboxScan(ctx, osStdout, items, key, previous, checkpoint, options.once)
			if err != nil || (options.once && emitted) {
				return err
			}
			nextRetry, retrying = options.interval, false
		}

		if err := sleepContext(ctx, delay); err != nil {
			return err
		}
	}
}

// Save a complete scan's visible observations after writing their events.
func publishMCPTaskInboxScan(ctx context.Context, output io.Writer, items []mcpTaskInboxEntry, key []byte, previous map[string]struct{}, checkpoint *mcpTaskInboxCheckpoint, once bool) (map[string]struct{}, bool, error) {
	current := make(map[string]struct{}, len(items))
	mac := hmac.New(sha256.New, key)
	emitted := false
	for _, entry := range items {
		body, err := json.Marshal(entry)
		if err != nil {
			return nil, false, err
		}
		mac.Reset()
		_, _ = mac.Write(body)
		digest := hex.EncodeToString(mac.Sum(nil))
		if _, seen := previous[digest]; seen {
			current[digest] = struct{}{}
			continue
		}
		if once {
			if emitted {
				// Leave later unseen entries eligible for the next invocation.
				continue
			}
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
		}
		line := append(body, '\n')
		n, err := output.Write(line)
		if err != nil {
			return nil, false, err
		}
		if n != len(line) {
			return nil, false, io.ErrShortWrite
		}
		current[digest] = struct{}{}
		emitted = true
	}
	// Only a complete scan can remove an entry from the visible set.
	if checkpoint != nil && (previous == nil || !maps.Equal(previous, current)) {
		if err := checkpoint.save(current); err != nil {
			return nil, false, err
		}
	}
	return current, emitted, nil
}

func scanMCPTaskInbox(ctx context.Context, client *mcphosting.Client, query mcphosting.TaskInboxQuery) ([]mcpTaskInboxEntry, mcphosting.Exchange, error) {
	var exchange mcphosting.Exchange
	pages := 0
	tasks, _, err := collectListPages(ctx, "", true, func(ctx context.Context, cursor string) ([]mcphosting.Task, string, error) {
		query.Cursor = cursor
		page, x, err := client.ListTaskInbox(ctx, query)
		exchange = x
		pages++
		if err != nil {
			return nil, "", err
		}
		if pages == maxMCPTaskInboxWatchPages && page.NextCursor != "" {
			return nil, "", fmt.Errorf("MCP Task inbox scan exceeded %d pages; narrow --status or increase --limit", maxMCPTaskInboxWatchPages)
		}
		return page.Tasks, page.NextCursor, nil
	})
	if err != nil {
		return nil, exchange, err
	}

	// If a Task moves between pages, keep its latest observation in this scan.
	items := make([]mcpTaskInboxEntry, 0, len(tasks))
	indices := make(map[string]int, len(tasks))
	for _, task := range tasks {
		entry := mcpTaskInboxSnapshot(task)
		if i, ok := indices[entry.TaskID]; ok {
			items[i] = entry
			continue
		}
		indices[entry.TaskID] = len(items)
		items = append(items, entry)
	}
	return items, exchange, nil
}

func mcpTaskInboxWatchRetryDelay(err error, delay time.Duration) time.Duration {
	var upstream *api.APIError
	if errors.As(err, &upstream) {
		now := time.Now()
		for _, value := range upstream.Problem.HasHeader("Retry-After") {
			delay = max(delay, api.WorkflowRetryAfter(value, now).Sub(now))
		}
	}
	return delay
}
