// Package issues normalizes failure evidence and defines versioned grouping.
// It has no transport or persistence dependencies.
package issues

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/redact"
)

const GroupingVersion = 1

var unstableValue = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f-]{27,}\b|\b[0-9]+\b`)
var traceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var spanIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// Normalize rejects invalid envelopes and sanitizes all persisted text before
// deriving a fingerprint. Full request bodies, locals, and arbitrary tags are
// deliberately absent from the envelope.
func Normalize(in api.IssueEvent, now time.Time, limits api.IssueLimits) (api.IssueEvent, string, string, error) {
	if _, err := uuid.Parse(in.EventID); err != nil {
		return in, "", "", errors.New("event_id must be a UUID")
	}
	if in.OccurredAt.IsZero() || in.OccurredAt.After(now.Add(api.IssueMaxClockSkew)) || in.OccurredAt.Before(now.AddDate(0, 0, -limits.RetentionDays)) {
		return in, "", "", errors.New("occurred_at must be within retention and clock-skew bounds")
	}
	if in.SourceKind == "" {
		in.SourceKind = "exception"
	}
	switch in.SourceKind {
	case "exception", "http", "runtime", "worker":
	default:
		return in, "", "", errors.New("invalid source_kind")
	}
	if in.TraceID != "" && (!traceIDPattern.MatchString(in.TraceID) || strings.Trim(in.TraceID, "0") == "") {
		return in, "", "", errors.New("invalid trace_id")
	}
	if in.SpanID != "" && (!spanIDPattern.MatchString(in.SpanID) || strings.Trim(in.SpanID, "0") == "") {
		return in, "", "", errors.New("invalid span_id")
	}
	if len(in.RequestID) > api.IssueMaxTypeBytes || !utf8.ValidString(in.RequestID) {
		return in, "", "", errors.New("request_id must be bounded UTF-8 text")
	}
	if in.InvocationID != "" {
		if _, err := uuid.Parse(in.InvocationID); err != nil {
			return in, "", "", errors.New("invocation_id must be a UUID")
		}
	}
	if len(in.ExceptionType) > api.IssueMaxTypeBytes || len(in.Route) > api.IssueMaxTypeBytes || len(in.FingerprintOverride) > api.IssueMaxTypeBytes || len(in.Frames) > api.IssueMaxFrames {
		return in, "", "", errors.New("exception type, route, fingerprint, or frame count exceeds the limit")
	}
	if in.HTTPStatus != 0 && (in.HTTPStatus < 400 || in.HTTPStatus > 599) {
		return in, "", "", errors.New("http_status must be 400..599")
	}
	if strings.TrimSpace(in.ExceptionType) == "" {
		return in, "", "", errors.New("exception_type is required")
	}
	in.Redactions = nil
	sanitize := func(value string, cap int) string {
		value, names := redact.New(cap).Apply(strings.ToValidUTF8(value, ""))
		in.Redactions = append(in.Redactions, names...)
		return value
	}
	in.ExceptionType = sanitize(strings.TrimSpace(in.ExceptionType), api.IssueMaxTypeBytes)
	in.Message = sanitize(in.Message, api.IssueMessageMaxBytes)
	in.StackTrace = sanitize(in.StackTrace, api.IssueStackMaxBytes)
	in.Route = sanitize(in.Route, api.IssueMaxTypeBytes)
	in.RequestID = sanitize(in.RequestID, api.IssueMaxTypeBytes)
	in.FingerprintOverride = sanitize(in.FingerprintOverride, api.IssueMaxTypeBytes)
	in.Frames = append([]api.IssueFrame(nil), in.Frames...)
	if len(in.Frames) == 0 {
		in.Frames = ParseStack(in.StackTrace)
	}
	for i := range in.Frames {
		f := &in.Frames[i]
		if f.Line < 0 || !utf8.ValidString(f.File) || !utf8.ValidString(f.Function) {
			return in, "", "", errors.New("invalid stack frame")
		}
		f.File = sanitize(f.File, api.IssueMaxFrameBytes)
		f.Function = sanitize(f.Function, api.IssueMaxFrameBytes)
	}
	parts := []string{fmt.Sprint(GroupingVersion), in.SourceKind, in.ExceptionType}
	if in.FingerprintOverride != "" {
		parts = append(parts, "override", in.FingerprintOverride)
	} else {
		frames := 0
		for _, f := range in.Frames {
			if f.InApp {
				parts = append(parts, groupingPath(f.File)+":"+f.Function)
				frames++
			}
		}
		if frames == 0 {
			parts = append(parts, "fallback", in.Route, fmt.Sprint(in.HTTPStatus), unstableValue.ReplaceAllString(in.Message, "<value>"))
		}
	}
	sort.Strings(in.Redactions)
	in.Redactions = compactRedactions(in.Redactions)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	title := in.ExceptionType
	if in.Message != "" {
		title += ": " + in.Message
	}
	title, _ = redact.New(api.IssueMessageMaxBytes).Apply(title)
	return in, hex.EncodeToString(sum[:]), title, nil
}

// PayloadDigest makes an exact retry idempotent while rejecting an event-ID
// reuse with a different submitted envelope. It never exposes submitted text.
func PayloadDigest(in api.IssueEvent) string {
	raw, _ := json.Marshal(in)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Recurs excludes late delivery and occurrences from older, co-serving releases.
func Recurs(issue api.Issue, deployment string, occurred time.Time, newerRelease ...bool) bool {
	sameOrNewer := deployment == issue.FixedDeploymentID || (len(newerRelease) > 0 && newerRelease[0])
	return issue.State == "resolved" && issue.ResolvedAt != nil && occurred.After(*issue.ResolvedAt) && sameOrNewer
}

// Preserve source directories so two same-named modules cannot collapse.
// Strip only documented container/build roots; use an override for other roots.
func groupingPath(file string) string {
	file = path.Clean(strings.ReplaceAll(strings.TrimPrefix(file, "file://"), "\\", "/"))
	if strings.HasPrefix(file, "/build/") {
		if _, rest, ok := strings.Cut(strings.TrimPrefix(file, "/build/"), "/"); ok {
			return rest
		}
	}
	for _, root := range []string{"/app/", "/workspace/", "/var/task/"} {
		if strings.HasPrefix(file, root) {
			return strings.TrimPrefix(file, root)
		}
	}
	return strings.TrimPrefix(file, "./")
}
func compactRedactions(in []string) []string {
	out := in[:0]
	for _, name := range in {
		if len(out) == 0 || out[len(out)-1] != name {
			out = append(out, name)
		}
	}
	return out
}
