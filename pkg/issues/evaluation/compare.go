package evaluation

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
)

// Decision describes selected grouping inputs without including their values.
// Case IDs are intentionally visible: use reviewed, non-sensitive identifiers.
type Decision struct {
	Basis             string `json:"basis"`
	SourceKind        string `json:"source_kind"`
	DiscriminatorKind string `json:"discriminator_kind"`
	FrameIndexes      []int  `json:"frame_indexes,omitempty"`
	CauseIndex        *int   `json:"cause_index,omitempty"`
}

type Assignment struct {
	ID       string   `json:"id"`
	Group    string   `json:"group"`
	Decision Decision `json:"decision"`
}

// Baseline is a portable, reviewable partition. A group is named by its smallest
// case ID, not by a live fingerprint. Strategy-version changes alone therefore
// do not pretend that every case's membership has changed.
type Baseline struct {
	Version      int          `json:"version"`
	CorpusSHA256 string       `json:"corpus_sha256"`
	Strategy     string       `json:"strategy"`
	Assignments  []Assignment `json:"assignments"`
}

type CaseChange struct {
	ID                string   `json:"id"`
	Change            string   `json:"change"`
	BaselineGroup     string   `json:"baseline_group"`
	CandidateGroup    string   `json:"candidate_group"`
	BaselineDecision  Decision `json:"baseline_decision"`
	CandidateDecision Decision `json:"candidate_decision"`
}

type Comparison struct {
	Version              int          `json:"version"`
	CorpusSHA256         string       `json:"corpus_sha256"`
	Baseline             Report       `json:"baseline"`
	Candidate            Report       `json:"candidate"`
	MergedPairs          int64        `json:"merged_pairs"`
	SplitPairs           int64        `json:"split_pairs"`
	NewFalseMergePairs   int64        `json:"new_false_merge_pairs"`
	FixedFalseMergePairs int64        `json:"fixed_false_merge_pairs"`
	NewFalseSplitPairs   int64        `json:"new_false_split_pairs"`
	FixedFalseSplitPairs int64        `json:"fixed_false_split_pairs"`
	Regressed            bool         `json:"regressed"`
	Changes              []CaseChange `json:"changes"`
}

// Snapshot evaluates a corpus without applying any live regrouping. Persisting
// this value is a review decision; generating it never marks it approved.
func Snapshot(cases []Case, strategy string, now time.Time) (Baseline, error) {
	rows, err := normalize(cases, strategy, now)
	if err != nil {
		return Baseline{}, err
	}
	digest, err := corpusDigest(cases)
	if err != nil {
		return Baseline{}, err
	}
	names := map[string]string{}
	for _, row := range rows {
		if name, ok := names[row.group]; !ok || row.id < name {
			names[row.group] = row.id
		}
	}
	baseline := Baseline{Version: 1, CorpusSHA256: digest, Strategy: strategy}
	for _, row := range rows {
		baseline.Assignments = append(baseline.Assignments, Assignment{ID: row.id, Group: names[row.group], Decision: row.decision})
	}
	slices.SortFunc(baseline.Assignments, func(a, b Assignment) int { return cmp.Compare(a.ID, b.ID) })
	return baseline, nil
}

func corpusDigest(cases []Case) (string, error) {
	ordered := slices.Clone(cases)
	slices.SortFunc(ordered, func(a, b Case) int { return cmp.Compare(a.ID, b.ID) })
	raw, err := json.Marshal(ordered)
	if err != nil {
		return "", fmt.Errorf("encode grouping corpus: %w", err)
	}
	if len(raw) > MaxCorpusBytes {
		return "", fmt.Errorf("canonical corpus exceeds byte bound")
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

// Compare checks exactly the baseline's corpus, then measures changes to pair
// membership using frequency intersections, without enumerating O(n²) pairs.
// Any newly incorrect pair is a regression, even if another pair improved.
func Compare(cases []Case, baseline Baseline, strategy string, now time.Time) (Comparison, error) {
	candidate, err := Snapshot(cases, strategy, now)
	if err != nil {
		return Comparison{}, err
	}
	if err := validateBaseline(cases, baseline, candidate.CorpusSHA256); err != nil {
		return Comparison{}, err
	}
	return comparePartitions(cases, baseline, candidate), nil
}

func validateBaseline(cases []Case, baseline Baseline, digest string) error {
	if baseline.Version != 1 || !validStrategy(baseline.Strategy) {
		return fmt.Errorf("unsupported grouping baseline version or strategy")
	}
	if baseline.CorpusSHA256 != digest {
		return fmt.Errorf("baseline corpus differs; review the corpus and create a new baseline explicitly")
	}
	if len(baseline.Assignments) != len(cases) {
		return fmt.Errorf("baseline must assign every corpus case exactly once")
	}
	byID := map[string]Case{}
	for _, c := range cases {
		byID[c.ID] = c
	}
	seen, minimum, namespaces := map[string]bool{}, map[string]string{}, map[string]string{}
	for _, assignment := range baseline.Assignments {
		c, ok := byID[assignment.ID]
		if !ok || seen[assignment.ID] || !validID(assignment.Group) || !validDecision(assignment.Decision, baseline.Strategy) {
			return fmt.Errorf("baseline has an unknown, duplicate or invalid assignment")
		}
		seen[assignment.ID] = true
		namespace := key(c.App, c.Environment)
		if previous, ok := namespaces[assignment.Group]; ok && previous != namespace {
			return fmt.Errorf("baseline groups must not cross app/environment namespaces")
		}
		namespaces[assignment.Group] = namespace
		if previous, ok := minimum[assignment.Group]; !ok || assignment.ID < previous {
			minimum[assignment.Group] = assignment.ID
		}
	}
	for group, first := range minimum {
		if group != first {
			return fmt.Errorf("baseline group must be named by its smallest member case id")
		}
	}
	return nil
}

func validDecision(d Decision, strategy string) bool {
	switch d.SourceKind {
	case "exception", "http", "runtime", "worker":
	default:
		return false
	}
	if d.CauseIndex != nil && (strategy != issues.CauseStrategy || *d.CauseIndex < 0 || *d.CauseIndex >= api.IssueMaxCauses) {
		return false
	}
	if len(d.FrameIndexes) > api.IssueMaxFrames || (strategy != "legacy" && len(d.FrameIndexes) > 1) {
		return false
	}
	seen := map[int]bool{}
	for _, index := range d.FrameIndexes {
		if index < 0 || index >= api.IssueMaxFrames || seen[index] {
			return false
		}
		seen[index] = true
	}
	if d.Basis == "override" {
		return d.DiscriminatorKind == "override" && len(d.FrameIndexes) == 0 && d.CauseIndex == nil
	}
	if d.Basis == "application-frames" {
		return strategy == "legacy" && d.DiscriminatorKind == "none" && len(d.FrameIndexes) > 0
	}
	if d.DiscriminatorKind != "message" && (d.DiscriminatorKind != "error-code" || strategy == "legacy") {
		return false
	}
	switch d.Basis {
	case "fallback":
		return len(d.FrameIndexes) == 0
	case "failure-site":
		return strategy != "legacy" && len(d.FrameIndexes) == 1
	default:
		return false
	}
}

func comparePartitions(cases []Case, baseline, candidate Baseline) Comparison {
	left := map[string]Assignment{}
	for _, a := range baseline.Assignments {
		left[a.ID] = a
	}
	labels := map[string]string{}
	for _, c := range cases {
		labels[c.ID] = key(c.App, c.Environment, c.Family)
	}
	var oldRows, newRows []normalizedCase
	joint, jointFamily := map[[2]string]int64{}, map[[3]string]int64{}
	oldSizes, newSizes := map[string]int64{}, map[string]int64{}
	for _, a := range candidate.Assignments {
		b, family := left[a.ID], labels[a.ID]
		oldRows = append(oldRows, normalizedCase{id: b.ID, group: b.Group, family: family})
		newRows = append(newRows, normalizedCase{id: a.ID, group: a.Group, family: family})
		joint[[2]string{b.Group, a.Group}]++
		jointFamily[[3]string{b.Group, a.Group, family}]++
		oldSizes[b.Group]++
		newSizes[a.Group]++
	}
	report := Comparison{
		Version: 1, CorpusSHA256: candidate.CorpusSHA256,
		Baseline: summarize(oldRows, baseline.Strategy), Candidate: summarize(newRows, candidate.Strategy),
		Changes: []CaseChange{},
	}
	var sharedSame, sharedTrue int64
	for _, n := range joint {
		sharedSame += pairs(n)
	}
	for _, n := range jointFamily {
		sharedTrue += pairs(n)
	}
	var oldSame, newSame int64
	for _, n := range oldSizes {
		oldSame += pairs(n)
	}
	for _, n := range newSizes {
		newSame += pairs(n)
	}
	report.MergedPairs, report.SplitPairs = newSame-sharedSame, oldSame-sharedSame
	report.NewFalseMergePairs = report.Candidate.FalseMergePairs - (sharedSame - sharedTrue)
	report.FixedFalseMergePairs = report.Baseline.FalseMergePairs - (sharedSame - sharedTrue)
	report.NewFalseSplitPairs = oldSame - report.Baseline.FalseMergePairs - sharedTrue
	report.FixedFalseSplitPairs = newSame - report.Candidate.FalseMergePairs - sharedTrue
	report.Regressed = report.NewFalseMergePairs > 0 || report.NewFalseSplitPairs > 0
	for _, a := range candidate.Assignments {
		b := left[a.ID]
		shared := joint[[2]string{b.Group, a.Group}]
		merges, splits := newSizes[a.Group] > shared, oldSizes[b.Group] > shared
		change := "merged-and-split"
		if !merges && !splits {
			continue
		} else if !merges {
			change = "split"
		} else if !splits {
			change = "merged"
		}
		report.Changes = append(report.Changes, CaseChange{
			ID: a.ID, Change: change, BaselineGroup: b.Group, CandidateGroup: a.Group,
			BaselineDecision: b.Decision, CandidateDecision: a.Decision,
		})
	}
	return report
}
