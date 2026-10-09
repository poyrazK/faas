package routelifecycle

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Finding struct {
	Method   string `json:"method,omitempty"`
	Path     string `json:"path,omitempty"`
	Code     string `json:"code"`
	Severity string `json:"severity"`
}
type Review struct {
	Version         int       `json:"version"`
	GeneratedAt     time.Time `json:"generated_at"`
	BaselineSHA256  string    `json:"baseline_sha256"`
	CandidateSHA256 string    `json:"candidate_sha256"`
	Outcome         string    `json:"outcome"`
	Findings        []Finding `json:"findings"`
}

func (r Review) Blocking() bool { return len(r.Findings) > 0 }

// Compare checks declarations only. A successor URL does not prove compatibility.
func Compare(before, after []byte, at time.Time) Review {
	r := Review{Version: 1, GeneratedAt: at.UTC(), BaselineSHA256: fmt.Sprintf("%x", sha256.Sum256(before)), CandidateSHA256: fmt.Sprintf("%x", sha256.Sum256(after)), Outcome: "clear", Findings: []Finding{}}
	add := func(key, code, severity string) {
		method, path, _ := strings.Cut(key, " ")
		r.Findings = append(r.Findings, Finding{method, path, code, severity})
	}
	old, err := declarationOperations(before)
	if err != nil {
		add("", "baseline_contract_unavailable_or_unsupported", "unknown")
	}
	next, err := declarationOperations(after)
	if err != nil {
		add("", "candidate_contract_unavailable_or_unsupported", "unknown")
	}
	for key, operation := range old {
		baseline, err := Parse(operation)
		if err != nil {
			add(key, "baseline_lifecycle_invalid", "unknown")
			continue
		}
		candidate, present := next[key]
		deprecated := operation["deprecated"] == true
		if !present {
			if !deprecated {
				continue
			}
			if baseline.SunsetAt.IsZero() {
				add(key, "deprecated_removal_without_sunset", "review_required")
			} else if at.Before(baseline.SunsetAt) {
				add(key, "operation_removed_before_sunset", "regression")
			}
			continue
		}
		proposed, parseErr := Parse(candidate)
		if parseErr != nil {
			add(key, "candidate_lifecycle_invalid", "unknown")
		}
		if deprecated && candidate["deprecated"] != true {
			add(key, "deprecation_removed", "regression")
		}
		for _, field := range []string{"x-gregale-deprecated-at", "x-gregale-sunset-at", "x-gregale-successor"} {
			if _, had := operation[field]; had {
				if _, has := candidate[field]; !has {
					add(key, "lifecycle_field_removed:"+field, "regression")
				}
			}
		}
		if parseErr == nil && !baseline.SunsetAt.IsZero() && !proposed.SunsetAt.IsZero() && proposed.SunsetAt.Before(baseline.SunsetAt) {
			add(key, "sunset_moved_earlier", "regression")
		}
		if baseline.Successor != proposed.Successor && proposed.Successor != "" {
			add(key, "successor_changed_requires_review", "review_required")
		}
	}
	for key, operation := range next {
		if _, exists := old[key]; !exists {
			if _, err := Parse(operation); err != nil {
				add(key, "candidate_lifecycle_invalid", "unknown")
			}
		}
	}
	sort.Slice(r.Findings, func(i, j int) bool {
		a, b := r.Findings[i], r.Findings[j]
		return a.Path+"\x00"+a.Method+"\x00"+a.Code < b.Path+"\x00"+b.Method+"\x00"+b.Code
	})
	for _, f := range r.Findings {
		switch f.Severity {
		case "unknown":
			r.Outcome = "incomplete"
		case "regression":
			if r.Outcome != "incomplete" {
				r.Outcome = "regressions"
			}
		default:
			if r.Outcome == "clear" {
				r.Outcome = "review_required"
			}
		}
	}
	return r
}
func declarationOperations(body []byte) (map[string]map[string]any, error) {
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return nil, fmt.Errorf("invalid document")
	}
	version, _ := root["openapi"].(string)
	paths, ok := root["paths"].(map[string]any)
	if !ok || (!strings.HasPrefix(version, "3.0.") && !strings.HasPrefix(version, "3.1.")) {
		return nil, fmt.Errorf("unsupported document")
	}
	out := map[string]map[string]any{}
	for path, value := range paths {
		item, ok := value.(map[string]any)
		if !ok || !strings.HasPrefix(path, "/") {
			return nil, fmt.Errorf("invalid path")
		}
		if _, ref := item["$ref"]; ref {
			return nil, fmt.Errorf("path reference unsupported")
		}
		for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"} {
			raw, present := item[method]
			if !present {
				continue
			}
			op, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid operation")
			}
			if _, ref := op["$ref"]; ref {
				return nil, fmt.Errorf("operation reference unsupported")
			}
			out[strings.ToUpper(method)+" "+path] = op
		}
	}
	return out, nil
}
