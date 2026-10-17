package issues

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

func normalizeContext(in *api.IssueEvent, sanitize func(string, int) string) error {
	if len(in.Breadcrumbs) > api.IssueBreadcrumbsMax || len(in.Tags) > api.IssueTagsMax || in.BreadcrumbsDropped < 0 || in.BreadcrumbsDropped > api.IssueFrameCoordinateMax {
		return errors.New("issue context exceeds its bounds")
	}
	breadcrumbs := make([]api.IssueBreadcrumb, 0, len(in.Breadcrumbs))
	for _, entry := range in.Breadcrumbs {
		switch entry.Category {
		case "http", "db", "cache", "workflow", "log", "custom":
		default:
			return errors.New("invalid breadcrumb category")
		}
		switch entry.Level {
		case "debug", "info", "warning", "error":
		default:
			return errors.New("invalid breadcrumb level")
		}
		if entry.OccurredAt.IsZero() || entry.OccurredAt.After(in.OccurredAt.Add(api.IssueMaxClockSkew)) || !utf8.ValidString(entry.Message) || strings.ContainsRune(entry.Message, 0) {
			return errors.New("invalid breadcrumb evidence")
		}
		if entry.OccurredAt.Before(in.OccurredAt.Add(-api.IssueBreadcrumbMaxAge)) {
			if in.BreadcrumbsDropped < api.IssueFrameCoordinateMax {
				in.BreadcrumbsDropped++
			}
			continue
		}
		entry.Message = boundedContextText(entry.Message, api.IssueBreadcrumbMessageMaxBytes, sanitize)
		breadcrumbs = append(breadcrumbs, entry)
	}
	in.Breadcrumbs = breadcrumbs
	tags := make(map[string]string, len(in.Tags))
	for key, value := range in.Tags {
		if len(key) > api.IssueTagKeyMaxBytes || !utf8.ValidString(key) || !utf8.ValidString(value) || strings.ContainsRune(key, 0) || strings.ContainsRune(value, 0) {
			return errors.New("invalid issue tag")
		}
		if !api.IsIssueTagKeyAllowed(key) {
			in.Redactions = append(in.Redactions, "issue_tag_allowlist")
			continue
		}
		tags[key] = boundedContextText(value, api.IssueTagValueMaxBytes, sanitize)
	}
	in.Tags = tags
	return nil
}

// The legacy sanitizer can append an ellipsis beyond its truncation budget.
// Context has strict byte caps independent of that legacy grouping behavior.
func boundedContextText(value string, limit int, sanitize func(string, int) string) string {
	value = sanitize(value, limit)
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}
