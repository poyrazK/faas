// Package obsrulecatalog owns explicitly selected expected rule definitions.
package obsrulecatalog

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/prometheus/promql/parser"
)

var ErrInvalidDefinition = errors.New("invalid or unsupported rule definition")
var safeIdentity = regexp.MustCompile(`^[a-zA-Z0-9_.:-]+$`)
var safeRuleName = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
var safeLabelName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
var safeHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Definition contains digests, never raw expressions or label values.
type Definition struct {
	ExpressionHash       string  `json:"expression_hash"`
	LabelsHash           string  `json:"labels_hash"`
	ForSeconds           float64 `json:"for_seconds"`
	KeepFiringForSeconds float64 `json:"keep_firing_for_seconds"`
	Fingerprint          string  `json:"fingerprint"`
}

func Identity(file, group, name string) (string, bool) {
	if !SafeIdentity(file) || !SafeIdentity(group) || len(name) > api.ObsAlertNameMaxBytes || !safeRuleName.MatchString(name) {
		return "", false
	}
	return digest([]string{file, group, name}), true
}

func SafeIdentity(value string) bool {
	return len(value) <= api.ObsRuleHealthIdentityMaxBytes && safeIdentity.MatchString(value)
}

// Normalize compares canonical PromQL syntax, hold/keep durations and rule
// labels. It does not prove semantic equivalence or verify metric inputs.
func Normalize(query string, hold, keep float64, labels map[string]string) (Definition, error) {
	if query == "" || len(query) > api.ObsRuleCoverageExpressionMaxBytes || !validDuration(hold) || !validDuration(keep) || len(labels) > api.ObsRuleCoverageLabelsMax {
		return Definition{}, ErrInvalidDefinition
	}
	normalizedLabels := map[string]string{}
	for key, value := range labels {
		if len(key) > api.ObsRuleCoverageLabelNameMaxBytes || !safeLabelName.MatchString(key) || len(value) > api.ObsRuleCoverageLabelValueMaxBytes {
			return Definition{}, ErrInvalidDefinition
		}
		normalizedLabels[key] = value
	}
	expr, err := parser.NewParser(parser.Options{}).ParseExpr(query)
	if err != nil {
		return Definition{}, ErrInvalidDefinition
	}
	parser.Inspect(expr, func(node parser.Node, _ []parser.Node) error {
		switch n := node.(type) {
		case *parser.VectorSelector:
			sort.Slice(n.LabelMatchers, func(i, j int) bool {
				a, b := n.LabelMatchers[i], n.LabelMatchers[j]
				if a.Name != b.Name {
					return a.Name < b.Name
				}
				if a.Type != b.Type {
					return a.Type < b.Type
				}
				return a.Value < b.Value
			})
		case *parser.AggregateExpr:
			sort.Strings(n.Grouping)
		case *parser.BinaryExpr:
			if n.VectorMatching != nil {
				sort.Strings(n.VectorMatching.MatchingLabels)
				sort.Strings(n.VectorMatching.Include)
			}
		}
		return nil
	})
	// JSON must not distinguish negative zero from a zero duration.
	if hold == 0 {
		hold = 0
	}
	if keep == 0 {
		keep = 0
	}
	d := Definition{ExpressionHash: digest(expr.String()), LabelsHash: digest(normalizedLabels), ForSeconds: hold, KeepFiringForSeconds: keep}
	d.Fingerprint = definitionFingerprint(d)
	return d, nil
}

func (d Definition) ChangedFields(other Definition) []string {
	out := []string{}
	if d.ExpressionHash != other.ExpressionHash {
		out = append(out, "expression")
	}
	if d.ForSeconds != other.ForSeconds {
		out = append(out, "for")
	}
	if d.KeepFiringForSeconds != other.KeepFiringForSeconds {
		out = append(out, "keep_firing_for")
	}
	if d.LabelsHash != other.LabelsHash {
		out = append(out, "labels")
	}
	return out
}

func definitionFingerprint(d Definition) string {
	return digest([]any{d.ExpressionHash, d.ForSeconds, d.KeepFiringForSeconds, d.LabelsHash})
}

func validDuration(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}
func digest(value any) string {
	encoded, _ := json.Marshal(value) // All call sites supply finite typed data.
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}
