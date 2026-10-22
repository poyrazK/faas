package issues

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type groupingDecision struct {
	parts       []string
	explanation api.IssueGroupingExplanation
}

// ExplainGrouping uses retained, already-sanitized evidence and the persisted
// group's version. It does not apply today's validation or redaction policy to
// history. Unknown versions or evidence that cannot reproduce the fingerprint
// return nil rather than claiming an unverified grouping reason.
func ExplainGrouping(event api.IssueEvent, version int, fingerprint string) *api.IssueGroupingExplanation {
	if version != GroupingVersion && version != FailureSiteGroupingVersion && version != CauseGroupingVersion {
		return nil
	}
	decision := decideGrouping(event, version)
	if groupingFingerprint(decision.parts, version) != fingerprint {
		return nil
	}
	return &decision.explanation
}

func groupingFingerprint(parts []string, version int) string {
	identity := []byte(strings.Join(parts, "\x1f"))
	if version >= FailureSiteGroupingVersion {
		identity, _ = json.Marshal(parts)
	}
	sum := sha256.Sum256(identity)
	return hex.EncodeToString(sum[:])
}

// decideGrouping is shared by ingestion and explanation. Keep version 1's
// separator-based identity and version 2's JSON tuple exactly unchanged.
func decideGrouping(in api.IssueEvent, version int) groupingDecision {
	if version == CauseGroupingVersion {
		var index *int
		if in.FingerprintOverride == "" && !in.CausesIncomplete && len(in.Causes) > 0 {
			i := len(in.Causes) - 1
			index = &i
			c := in.Causes[i]
			in.ExceptionType, in.Message, in.ErrorCode, in.Frames, in.FrameOrder = c.ExceptionType, c.Message, c.ErrorCode, c.Frames, c.FrameOrder
		}
		out := decideGrouping(in, FailureSiteGroupingVersion)
		out.parts[0] = fmt.Sprint(version)
		out.explanation.GroupingVersion, out.explanation.Strategy, out.explanation.CauseIndex = version, CauseStrategy, index
		return out
	}
	out := groupingDecision{
		parts: []string{fmt.Sprint(version), in.SourceKind, in.ExceptionType},
		explanation: api.IssueGroupingExplanation{
			GroupingVersion: version, Strategy: "legacy", SourceKind: in.SourceKind,
			ExceptionType: in.ExceptionType, DiscriminatorKind: "none",
		},
	}
	if version == FailureSiteGroupingVersion {
		out.explanation.Strategy = FailureSiteStrategy
	}
	if in.FingerprintOverride != "" {
		out.parts = append(out.parts, "override", in.FingerprintOverride)
		out.explanation.Basis = "override"
		out.explanation.DiscriminatorKind = "override"
		out.explanation.DiscriminatorValue = in.FingerprintOverride
		return out
	}
	if version == GroupingVersion {
		for i, f := range in.Frames {
			if f.InApp {
				file := groupingPath(f.File)
				out.parts = append(out.parts, file+":"+f.Function)
				out.explanation.Frames = append(out.explanation.Frames, api.IssueGroupingFrame{FrameIndex: i, File: file, Function: f.Function})
			}
		}
		if len(out.explanation.Frames) > 0 {
			out.explanation.Basis = "application-frames"
			return out
		}
	} else {
		start, end, step := 0, len(in.Frames), 1
		if in.FrameOrder == "oldest-first" {
			start, end, step = len(in.Frames)-1, -1, -1
		}
		for i := start; i != end; i += step {
			f := in.Frames[i]
			if !f.InApp || (f.File == "" && f.Function == "") {
				continue
			}
			function := strings.TrimSpace(f.Function)
			if function == "" {
				function = "<anonymous>"
			}
			file := groupingPath(f.File)
			out.parts = append(out.parts, "frame", file, function)
			out.explanation.Basis = "failure-site"
			out.explanation.Frames = []api.IssueGroupingFrame{{FrameIndex: i, File: file, Function: function}}
			break
		}
	}
	if len(out.explanation.Frames) == 0 {
		out.explanation.Basis = "fallback"
		out.explanation.Route, out.explanation.HTTPStatus = in.Route, in.HTTPStatus
		out.parts = append(out.parts, "fallback", in.Route, fmt.Sprint(in.HTTPStatus))
	}
	if version == FailureSiteGroupingVersion && in.ErrorCode != "" {
		out.parts = append(out.parts, "code", in.ErrorCode)
		out.explanation.DiscriminatorKind, out.explanation.DiscriminatorValue = "error-code", in.ErrorCode
		return out
	}
	message := unstableValue.ReplaceAllString(in.Message, "<value>")
	if version == FailureSiteGroupingVersion {
		message = strings.Join(strings.Fields(message), " ")
		out.parts = append(out.parts, "message", message)
	} else {
		out.parts = append(out.parts, message)
	}
	out.explanation.DiscriminatorKind, out.explanation.DiscriminatorValue = "message", message
	return out
}
