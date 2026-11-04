// Package issues normalizes failure evidence and defines versioned grouping.
// It has no transport or persistence dependencies.
package issues

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/redact"
	"github.com/onebox-faas/faas/pkg/safetext"
)

const (
	// GroupingVersion is the unchanged default for existing reporters and groups.
	GroupingVersion            = 1
	FailureSiteStrategy        = "failure-site-v1"
	FailureSiteGroupingVersion = 2
	CauseStrategy              = "cause-chain-v1"
	CauseGroupingVersion       = 3
)

// GroupingVersionFor must be called with a successfully normalized event.
func GroupingVersionFor(in api.IssueEvent) int {
	if in.GroupingStrategy == CauseStrategy {
		return CauseGroupingVersion
	}
	if in.GroupingStrategy == FailureSiteStrategy {
		return FailureSiteGroupingVersion
	}
	return GroupingVersion
}

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
	switch in.GroupingStrategy {
	case "", "legacy", FailureSiteStrategy, CauseStrategy:
	default:
		return in, "", "", errors.New("invalid grouping_strategy")
	}
	switch in.FrameOrder {
	case "", "newest-first", "oldest-first":
	default:
		return in, "", "", errors.New("invalid frame_order")
	}
	if in.Symbolication != nil {
		return in, "", "", errors.New("symbolication is read-only evidence")
	}
	if in.SymbolicationStrategy != "" && (in.SymbolicationStrategy != DeploymentSourceMapStrategy || (in.GroupingStrategy != FailureSiteStrategy && in.GroupingStrategy != CauseStrategy)) {
		return in, "", "", errors.New("deployment-source-maps-v1 requires failure-site-v1 or cause-chain-v1")
	}
	if len(in.ErrorCode) > api.IssueMaxTypeBytes || !utf8.ValidString(in.ErrorCode) {
		return in, "", "", errors.New("error_code must be bounded UTF-8 text")
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
		// PostgreSQL text and jsonb reject U+0000, even when JSON-escaped.
		// Preserve the surrounding evidence without making the event a
		// permanently retrying poison pill for reporter queues.
		value = strings.ReplaceAll(strings.ToValidUTF8(value, ""), "\x00", "\uFFFD")
		value, names := redact.New(cap).Apply(value)
		in.Redactions = append(in.Redactions, names...)
		return value
	}
	in.ExceptionType = sanitize(strings.TrimSpace(in.ExceptionType), api.IssueMaxTypeBytes)
	in.Message = sanitize(in.Message, api.IssueMessageMaxBytes)
	in.StackTrace = sanitize(in.StackTrace, api.IssueStackMaxBytes)
	in.Route = sanitize(in.Route, api.IssueMaxTypeBytes)
	// A canonical UUID is structured request identity. Its numeric segments
	// can match the card scrubber, which would break request-audit attribution.
	// Other opaque request context still passes through the PII scrubber.
	requestUUID, requestUUIDErr := uuid.Parse(in.RequestID)
	if requestUUIDErr != nil || !strings.EqualFold(requestUUID.String(), in.RequestID) {
		in.RequestID = sanitize(in.RequestID, api.IssueMaxTypeBytes)
	}
	in.FingerprintOverride = sanitize(in.FingerprintOverride, api.IssueMaxTypeBytes)
	in.ErrorCode = sanitize(strings.TrimSpace(in.ErrorCode), api.IssueMaxTypeBytes)
	if err := normalizeContext(&in, sanitize); err != nil {
		return in, "", "", err
	}
	// Validate breadcrumb times against the reported occurrence before
	// clamping accepted future timestamps for alert-window evaluation.
	if in.OccurredAt.After(now) {
		in.OccurredAt = now
	}
	in.Frames = append([]api.IssueFrame(nil), in.Frames...)
	if len(in.Frames) == 0 {
		if in.SymbolicationStrategy == DeploymentSourceMapStrategy {
			var order string
			in.Frames, order = parseStack(in.StackTrace, true, true)
			if in.FrameOrder == "" {
				in.FrameOrder = order
			}
		} else if in.GroupingStrategy == FailureSiteStrategy || in.GroupingStrategy == CauseStrategy {
			var order string
			in.Frames, order = parseFailureSiteStack(in.StackTrace)
			if in.FrameOrder == "" {
				in.FrameOrder = order
			}
		} else {
			in.Frames = ParseStack(in.StackTrace)
		}
	}
	for i := range in.Frames {
		f := &in.Frames[i]
		if f.Line < 0 || f.Column < 0 || f.Column > api.IssueFrameCoordinateMax || !utf8.ValidString(f.File) || !utf8.ValidString(f.Function) {
			return in, "", "", errors.New("invalid stack frame")
		}
		f.File = sanitize(f.File, api.IssueMaxFrameBytes)
		f.Function = sanitize(f.Function, api.IssueMaxFrameBytes)
	}
	if err := normalizeCauses(&in, sanitize); err != nil {
		return in, "", "", err
	}
	sort.Strings(in.Redactions)
	in.Redactions = compactRedactions(in.Redactions)
	fingerprint, title := groupingResult(in)
	return in, fingerprint, title, nil
}

func groupingResult(in api.IssueEvent) (string, string) {
	decision := decideGrouping(in, GroupingVersionFor(in))
	fingerprint := groupingFingerprint(decision.parts, decision.explanation.GroupingVersion)
	title := in.ExceptionType
	if in.Message != "" {
		title += ": " + in.Message
	}
	title, _ = redact.New(api.IssueMessageMaxBytes).Apply(title)
	// Redaction and its truncation marker can expand the output past the
	// database title limit even when each submitted field is within bounds.
	title = safetext.Truncate(title, api.IssueMessageMaxBytes)
	return fingerprint, title
}

// boundedMappedFrame bounds the JSON representation as well as the UTF-8
// string. A source map may repeat control characters across hundreds of
// frames, and JSON escaping can otherwise multiply the stored payload.
func boundedMappedFrame(value string) string {
	used := 2 // JSON string quotes
	for i, r := range value {
		n := utf8.RuneLen(r)
		switch r {
		case '"', '\\', '\b', '\f', '\n', '\r', '\t':
			n = 2
		case '<', '>', '&', '\u2028', '\u2029':
			n = 6
		default:
			if r < 0x20 {
				n = 6
			}
		}
		if used+n > api.IssueMaxFrameBytes {
			return value[:i]
		}
		used += n
	}
	return value
}

// NormalizeSymbolicated sanitizes newly mapped frame text without validating or
// truncating the already normalized envelope a second time. Redaction can
// expand a valid input beyond its original input limit.
func NormalizeSymbolicated(original, mapped api.IssueEvent) (api.IssueEvent, string, string, error) {
	if len(original.Frames) != len(mapped.Frames) || len(original.Causes) != len(mapped.Causes) {
		return original, "", "", errors.New("symbolication changed issue frame structure")
	}
	out := original
	out.Redactions = append([]string(nil), original.Redactions...)
	sanitize := func(value string) string {
		value = strings.ReplaceAll(strings.ToValidUTF8(value, ""), "\x00", "\uFFFD")
		value, names := redact.New(api.IssueMaxFrameBytes).Apply(value)
		out.Redactions = append(out.Redactions, names...)
		return boundedMappedFrame(value)
	}
	frames := func(before, after []api.IssueFrame) ([]api.IssueFrame, error) {
		if len(before) != len(after) {
			return nil, errors.New("symbolication changed issue frame structure")
		}
		result := append([]api.IssueFrame(nil), after...)
		for i := range result {
			f := &result[i]
			if f.Line < 0 || f.Column < 0 || f.Column > api.IssueFrameCoordinateMax || !utf8.ValidString(f.File) || !utf8.ValidString(f.Function) {
				return nil, errors.New("invalid symbolicated stack frame")
			}
			if f.File != before[i].File {
				f.File = sanitize(f.File)
			}
			if f.Function != before[i].Function {
				f.Function = sanitize(f.Function)
			}
		}
		return result, nil
	}
	var err error
	out.Frames, err = frames(original.Frames, mapped.Frames)
	if err != nil {
		return original, "", "", err
	}
	out.Causes = append([]api.IssueCause(nil), original.Causes...)
	for i := range out.Causes {
		out.Causes[i].Frames, err = frames(original.Causes[i].Frames, mapped.Causes[i].Frames)
		if err != nil {
			return original, "", "", err
		}
	}
	sort.Strings(out.Redactions)
	out.Redactions = compactRedactions(out.Redactions)
	fingerprint, title := groupingResult(out)
	return out, fingerprint, title, nil
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
