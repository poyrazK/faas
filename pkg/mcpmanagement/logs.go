package mcpmanagement

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apislogs"
	"github.com/onebox-faas/faas/pkg/safetext"
)

type logsArgs struct {
	App          string `json:"app" jsonschema:"Gregale app slug"`
	DeploymentID string `json:"deployment_id,omitempty" jsonschema:"Optional deployment ID; subject to plan permissions"`
	Limit        int    `json:"limit,omitempty" jsonschema:"Maximum log entries from 1 to 100; default 20"`
	Level        string `json:"level,omitempty" jsonschema:"Optional severity floor: info, warn, or error"`
	Grep         string `json:"grep,omitempty" jsonschema:"Literal substring filter up to 256 bytes"`
	Since        string `json:"since,omitempty" jsonschema:"Optional RFC3339 timestamp lower bound"`
}

type logEntry struct {
	api.LogEvent
	LineTruncated bool `json:"line_truncated,omitempty"`
}

type logSnapshot struct {
	Items     []logEntry        `json:"items"`
	Gaps      []api.LogGapEvent `json:"gaps,omitempty"`
	Truncated bool              `json:"truncated"`
}

func validateLogs(args logsArgs) error {
	if err := validateSegment(args.App, "app"); err != nil {
		return err
	}
	if args.DeploymentID != "" {
		if err := validateSegment(args.DeploymentID, "deployment_id"); err != nil {
			return err
		}
	}
	if args.Level != "" && !api.IsValidLogLevel(args.Level) {
		return invalid("invalid_argument", "level must be info, warn, or error")
	}
	if len(args.Grep) > apislogs.MaxGrepPatternBytes || strings.ContainsAny(args.Grep, "\r\n\x00") {
		return invalid("invalid_argument", "grep must be a single line of at most 256 bytes")
	}
	if args.Since != "" {
		if _, err := time.Parse(time.RFC3339Nano, args.Since); err != nil {
			return invalid("invalid_argument", "since must be an RFC3339 timestamp")
		}
	}
	return nil
}

func (m *management) getLogs(ctx context.Context, args logsArgs) (any, error) {
	if m.opts.Environments != nil && args.DeploymentID == "" {
		return nil, invalid("deployment_required", "environment-restricted connections require an exact consented deployment ID for logs")
	}
	if err := validateLogs(args); err != nil {
		return nil, err
	}
	limit, err := pageLimit(args.Limit)
	if err != nil {
		return nil, err
	}
	body, err := m.client.StreamAppLogs(ctx, args.App, args.DeploymentID, false, api.LogFilter{Level: args.Level, Grep: args.Grep, Since: args.Since})
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(io.LimitReader(body, maxLogBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read log snapshot: %w", err)
	}
	if len(data) > maxLogBytes {
		return nil, invalid("response_too_large", "Log snapshot exceeds the read bound; narrow the timestamp or substring filter")
	}
	return decodeLogs(ctx, data, limit, m.clean)
}

func decodeLogs(ctx context.Context, data []byte, limit int, clean func(string) string) (logSnapshot, error) {
	result := logSnapshot{Items: make([]logEntry, 0, limit)}
	decoder := api.NewDecoder(bytes.NewReader(data))
	// The input is already byte-bounded and buffered. Drain on early returns so
	// the SDK parser cannot remain blocked sending a later event to its channel.
	defer func() {
		for range decoder.Events() {
		}
		_ = decoder.Close()
	}()
	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case event, ok := <-decoder.Events():
			if !ok {
				if err := <-decoder.Errors(); err != nil && !errors.Is(err, io.EOF) {
					return result, fmt.Errorf("decode log snapshot: %w", err)
				}
				return result, nil
			}
			if err := appendLogEvent(&result, event, limit, clean); err != nil {
				return result, err
			}
		}
	}
}

func appendLogEvent(result *logSnapshot, event api.Event, limit int, clean func(string) string) error {
	switch event.Event {
	case "log":
		var line api.LogEvent
		if json.Unmarshal([]byte(event.Data), &line) != nil {
			return invalid("invalid_response", "API returned a malformed log entry")
		}
		if len(result.Items) == limit {
			copy(result.Items, result.Items[1:])
			result.Items = result.Items[:limit-1]
			result.Truncated = true
		}
		line.Line = clean(line.Line)
		truncated := len(line.Line) > maxLogLineBytes
		line.Line = safetext.Truncate(line.Line, maxLogLineBytes)
		result.Items = append(result.Items, logEntry{line, truncated})
		result.Truncated = result.Truncated || truncated
	case "gap":
		var gap api.LogGapEvent
		if json.Unmarshal([]byte(event.Data), &gap) != nil {
			return invalid("invalid_response", "API returned a malformed log gap")
		}
		if len(result.Gaps) < limit {
			result.Gaps = append(result.Gaps, gap)
		} else {
			result.Truncated = true
		}
	case "error":
		var p api.Problem
		if json.Unmarshal([]byte(event.Data), &p) != nil || p.Code == "" {
			return invalid("logs_unavailable", "API reported a log stream error")
		}
		return &api.APIError{Problem: p}
	case "degraded":
		return invalid("logs_unavailable", "Log backend is degraded; retry the snapshot later")
	}
	return nil
}
