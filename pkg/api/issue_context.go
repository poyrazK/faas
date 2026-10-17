package api

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

type IssueBreadcrumb struct {
	OccurredAt time.Time `json:"occurred_at"`
	Category   string    `json:"category"`
	Level      string    `json:"level"`
	Message    string    `json:"message"`
}

// These operational dimensions never supply verified customer identity.
func IsIssueTagKeyAllowed(key string) bool {
	switch key {
	case "component", "operation", "feature_flag", "feature_variant", "dependency", "region", "job_type", "workflow", "state", "error_class", "release_channel", "runtime":
		return true
	default:
		return false
	}
}

func ParseIssueTagFilter(value string) (map[string]string, error) {
	if value == "" {
		return map[string]string{}, nil
	}
	key, text, ok := strings.Cut(value, "=")
	if !ok || !IsIssueTagKeyAllowed(key) || len(text) > IssueTagValueMaxBytes || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return nil, errors.New("tag must be an allowed operational key=value pair")
	}
	return map[string]string{key: text}, nil
}
