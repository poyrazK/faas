// Package evaluation measures grouping against explicitly labeled examples.
// It does not infer incident families or claim production grouping accuracy.
package evaluation

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
)

const MaxCases = 10000
const MaxCorpusBytes = 8 << 20

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
	report := Report{Strategy: strategy, Cases: len(cases), Pairs: pairs(int64(len(cases)))}
	if len(cases) == 0 || len(cases) > MaxCases {
		return report, fmt.Errorf("corpus must contain 1–%d cases", MaxCases)
	}
	if strategy != "legacy" && strategy != issues.FailureSiteStrategy && strategy != issues.CauseStrategy {
		return report, fmt.Errorf("unsupported grouping strategy")
	}
	seen := map[string]bool{}
	groups := map[string]int64{}
	families := map[string]int64{}
	intersections := map[[2]string]int64{}
	for i, c := range cases {
		if c.ID == "" || c.Family == "" || seen[c.ID] {
			return report, fmt.Errorf("case %d needs unique id and nonempty family", i)
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
		_, fp, _, err := issues.Normalize(event, now, api.PlanScale.IssueLimits())
		if err != nil {
			return report, fmt.Errorf("case %d rejected: %w", i, err)
		}
		group := key(c.App, c.Environment, fp)
		family := key(c.App, c.Environment, c.Family)
		groups[group]++
		families[family]++
		intersections[[2]string{group, family}]++
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
	return report, nil
}
