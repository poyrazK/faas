package issues

import (
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
)

func ownershipPathMatches(file, prefix string) bool {
	return strings.HasPrefix(file, prefix) && (len(file) == len(prefix) || strings.HasSuffix(prefix, "/") || file[len(prefix)] == '/')
}

// OwnershipRuleMatches evaluates reported operational evidence, independently
// of grouping identity or verified customer attribution. All matchers are ANDed.
func OwnershipRuleMatches(rule api.IssueOwnershipRule, event api.IssueEvent) bool {
	if rule.ExceptionType != "" && rule.ExceptionType != event.ExceptionType || rule.SourceKind != "" && rule.SourceKind != event.SourceKind {
		return false
	}
	if rule.RoutePrefix != "" && !ownershipPathMatches(event.Route, rule.RoutePrefix) {
		return false
	}
	if rule.Tag != "" {
		key, value, _ := strings.Cut(rule.Tag, "=")
		actual, present := event.Tags[key]
		if !present || actual != value {
			return false
		}
	}
	if rule.SourcePathPrefix == "" {
		return true
	}
	matchFrames := func(frames []api.IssueFrame) bool {
		for _, frame := range frames {
			if !frame.InApp {
				continue
			}
			file, err := api.NormalizeIssueSourceFilePath(frame.File)
			if err == nil && ownershipPathMatches(file, rule.SourcePathPrefix) {
				return true
			}
		}
		return false
	}
	if matchFrames(event.Frames) {
		return true
	}
	for _, cause := range event.Causes {
		if matchFrames(cause.Frames) {
			return true
		}
	}
	return false
}
