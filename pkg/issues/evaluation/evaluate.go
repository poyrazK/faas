// Package evaluation measures grouping against explicitly labeled examples.
// It does not infer incident families or claim production grouping accuracy.
package evaluation

import (
	"encoding/json"
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
)

const MaxCases = api.IssueGroupingEvaluationCasesMax
const MaxCorpusBytes = api.IssueGroupingEvaluationCorpusMaxBytes

type Case struct {
	ID          string         `json:"id"`
	Family      string         `json:"family"`
	App         string         `json:"app,omitempty"`
	Environment string         `json:"environment,omitempty"`
	Event       api.IssueEvent `json:"event"`
}
type Report struct {
	Strategy        string   `json:"strategy"`
	Cases           int      `json:"cases"`
	Groups          int      `json:"groups"`
	Pairs           int64    `json:"pairs"`
	FalseMergePairs int64    `json:"false_merge_pairs"`
	FalseSplitPairs int64    `json:"false_split_pairs"`
	Precision       *float64 `json:"same_family_precision,omitempty"`
	Recall          *float64 `json:"same_family_recall,omitempty"`
}

func pairs(n int64) int64         { return n * (n - 1) / 2 }
func key(values ...string) string { raw, _ := json.Marshal(values); return string(raw) }

// Evaluate uses the real grouping normalizer on reported evidence. Capture-time
// validation, transport, source-map correctness and incident truth are outside
// this offline evaluation: IDs/timestamps are synthesized, and read-only mapping
// stamps are removed. Mapped frames themselves remain unchanged.
func Evaluate(cases []Case, strategy string, now time.Time) (Report, error) {
	rows, err := normalize(cases, strategy, now)
	if err != nil {
		return Report{}, err
	}
	return summarize(rows, strategy), nil
}

type normalizedCase struct {
	id, group, family string
	decision          Decision
}

func validStrategy(strategy string) bool {
	return strategy == "legacy" || strategy == issues.FailureSiteStrategy || strategy == issues.CauseStrategy
}

func normalize(cases []Case, strategy string, now time.Time) ([]normalizedCase, error) {
	if len(cases) == 0 || len(cases) > MaxCases {
		return nil, fmt.Errorf("corpus must contain 1–%d cases", MaxCases)
	}
	if !validStrategy(strategy) {
		return nil, fmt.Errorf("unsupported grouping strategy")
	}
	rows := make([]normalizedCase, 0, len(cases))
	seen := map[string]bool{}
	for i, c := range cases {
		if !validID(c.ID) || c.Family == "" || seen[c.ID] {
			return nil, fmt.Errorf("case %d needs unique bounded printable id and nonempty family", i)
		}
		seen[c.ID] = true
		event := c.Event
		event.EventID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(c.ID)).String()
		event.OccurredAt = now
		// Operational context does not participate in grouping; historical
		// breadcrumb times must not invalidate an offline identity fixture.
		event.Breadcrumbs = nil
		event.BreadcrumbsDropped = 0
		event.Tags = nil
		event.Symbolication = nil
		event.SymbolicationStrategy = ""
		event.GroupingStrategy = strategy
		event, fp, _, err := issues.Normalize(event, now, api.PlanScale.IssueLimits())
		if err != nil {
			return nil, fmt.Errorf("case %d rejected: %w", i, err)
		}
		explanation := issues.ExplainGrouping(event, issues.GroupingVersionFor(event), fp)
		if explanation == nil {
			return nil, fmt.Errorf("case %d grouping explanation could not be verified", i)
		}
		decision := Decision{
			Basis: explanation.Basis, SourceKind: explanation.SourceKind,
			DiscriminatorKind: explanation.DiscriminatorKind, CauseIndex: explanation.CauseIndex,
		}
		for _, frame := range explanation.Frames {
			decision.FrameIndexes = append(decision.FrameIndexes, frame.FrameIndex)
		}
		rows = append(rows, normalizedCase{
			id: c.ID, group: key(c.App, c.Environment, fp),
			family: key(c.App, c.Environment, c.Family), decision: decision,
		})
	}
	return rows, nil
}

func validID(id string) bool {
	if id == "" || len(id) > api.IssueGroupingEvaluationCaseIDMaxBytes || !utf8.ValidString(id) {
		return false
	}
	for _, r := range id {
		if !unicode.IsPrint(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func summarize(rows []normalizedCase, strategy string) Report {
	report := Report{Strategy: strategy, Cases: len(rows), Pairs: pairs(int64(len(rows)))}
	groups := map[string]int64{}
	families := map[string]int64{}
	intersections := map[[2]string]int64{}
	for _, row := range rows {
		groups[row.group]++
		families[row.family]++
		intersections[[2]string{row.group, row.family}]++
	}
	var actualSame, expectedSame, trueSame int64
	for _, n := range groups {
		actualSame += pairs(n)
	}
	for _, n := range families {
		expectedSame += pairs(n)
	}
	for _, n := range intersections {
		trueSame += pairs(n)
	}
	report.Groups = len(groups)
	report.FalseMergePairs = actualSame - trueSame
	report.FalseSplitPairs = expectedSame - trueSame
	if actualSame > 0 {
		value := float64(trueSame) / float64(actualSame)
		report.Precision = &value
	}
	if expectedSame > 0 {
		value := float64(trueSame) / float64(expectedSame)
		report.Recall = &value
	}
	return report
}
