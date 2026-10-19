// Package automationgate evaluates retained live-suite evidence without executing
// workflows or checking assertions. Its strict policy is versioned for CI users.
package automationgate

import (
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	Policy           = "strict_v1"
	ExitPassed       = 0
	ExitFailed       = 1
	ExitInconclusive = 7
)

type Target struct {
	Version          int64  `json:"version"`
	PublishedVersion int64  `json:"published_version"`
	DeploymentID     string `json:"deployment_id"`
}

type Selection struct {
	Name, BaselineID, CandidateID string
	Coverage                      *CoveragePolicy
	Expected                      Target
}

type Result struct {
	Outcome              string       `json:"outcome"`
	ExitCode             int          `json:"exit_code"`
	Reasons              []string     `json:"reasons"`
	CandidatePassed      int          `json:"candidate_passed"`
	CandidateFailed      int          `json:"candidate_failed"`
	MissingRequiredCases []string     `json:"missing_required_cases,omitempty"`
	EvidenceAge          *EvidenceAge `json:"evidence_age,omitempty"`
}

func (s Selection) ValidateCandidate() error {
	if !validName(s.Name, api.AutomationNameMaxBytes) || !validID(s.CandidateID) || !validID(s.Expected.DeploymentID) || s.Expected.Version <= 0 || s.Expected.PublishedVersion <= 0 || s.Expected.PublishedVersion > s.Expected.Version {
		return errors.New("provide an automation name, candidate suite UUID and positive expected saved/published versions and deployment UUID")
	}
	if s.Coverage != nil {
		return s.Coverage.Validate()
	}
	return nil
}
func (s Selection) Validate() error {
	if err := s.ValidateCandidate(); err != nil {
		return err
	}
	if !validID(s.BaselineID) || sameID(s.BaselineID, s.CandidateID) {
		return errors.New("provide distinct baseline and candidate suite UUIDs")
	}
	return nil
}

// Evaluate requires a coherent server comparison. A failure in an intended,
// current candidate with usable age evidence takes precedence over coverage gaps.
// Wrong/outdated targets and unusable age evidence are inconclusive regardless
// of the original candidate's verdicts.
func Evaluate(s Selection, view api.AutomationLiveSuiteComparison) (Result, error) {
	return EvaluateAt(s, view, time.Now().UTC())
}

// EvaluateAt uses the supplied evaluation clock, never the response observation
// time, to prevent replaying an old comparison from refreshing evidence.
func EvaluateAt(s Selection, view api.AutomationLiveSuiteComparison, now time.Time) (Result, error) {
	return evaluate(s, view, now, false)
}

// EvaluatePromotionAt validates a suite against itself for deliberate baseline
// promotion. It does not certify a regression comparison or relax gate identity.
func EvaluatePromotionAt(s Selection, view api.AutomationLiveSuiteComparison, now time.Time) (Result, error) {
	if !sameID(s.BaselineID, s.CandidateID) {
		return Result{}, errors.New("promotion must evaluate the selected suite against itself")
	}
	return evaluate(s, view, now, true)
}

func evaluate(s Selection, view api.AutomationLiveSuiteComparison, now time.Time, promotion bool) (Result, error) {
	validation := s.Validate
	if promotion {
		validation = s.ValidateCandidate
	}
	if err := validation(); err != nil {
		return Result{}, err
	}
	if err := validateComparison(s, view); err != nil {
		return Result{}, err
	}
	result := Result{Outcome: "passed", ExitCode: ExitPassed, Reasons: []string{}}
	target := view.Candidate
	if target.Version != s.Expected.Version || target.PublishedVersion != s.Expected.PublishedVersion || !sameID(target.DeploymentID, s.Expected.DeploymentID) {
		result.Reasons = append(result.Reasons, "candidate_target_mismatch")
	}
	if view.CandidateOutdated {
		result.Reasons = append(result.Reasons, "candidate_outdated")
	}
	targetBlocked := len(result.Reasons) > 0
	var incomplete bool
	for _, entry := range view.Cases {
		if entry.Candidate == nil {
			continue
		}
		switch entry.Candidate.Result {
		case "passed":
			result.CandidatePassed++
		case "failed":
			result.CandidateFailed++
		default:
			incomplete = true
		}
	}
	if result.CandidateFailed > 0 {
		result.Reasons = append(result.Reasons, "candidate_failed")
	}
	if view.Summary.Regressions > 0 {
		result.Reasons = append(result.Reasons, "regressions")
	}
	if incomplete {
		result.Reasons = append(result.Reasons, "candidate_incomplete")
	}
	if view.Summary.Unknown > 0 {
		result.Reasons = append(result.Reasons, "unknown_results")
	}
	if view.Summary.Incompatible > 0 {
		result.Reasons = append(result.Reasons, "incompatible_cases")
	}
	if view.Summary.Removed > 0 {
		result.Reasons = append(result.Reasons, "removed_cases")
	}
	if s.Coverage != nil {
		present := make(map[string]bool)
		for _, entry := range view.Cases {
			if entry.Candidate != nil {
				present[entry.Name] = true
			}
		}
		for _, name := range s.Coverage.RequiredCases {
			if !present[name] {
				result.MissingRequiredCases = append(result.MissingRequiredCases, name)
			}
		}
		sort.Strings(result.MissingRequiredCases)
		if len(result.MissingRequiredCases) > 0 {
			result.Reasons = append(result.Reasons, "required_cases_missing")
		}
	}
	ageBlocked := evaluateAge(s.Coverage, view.Cases, now, &result)
	switch {
	case targetBlocked || ageBlocked:
		result.Outcome, result.ExitCode = "inconclusive", ExitInconclusive
	case result.CandidateFailed > 0:
		result.Outcome, result.ExitCode = "failed", ExitFailed
	case len(result.Reasons) > 0:
		result.Outcome, result.ExitCode = "inconclusive", ExitInconclusive
	}
	return result, nil
}

func validID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil
}

func sameID(a, b string) bool {
	left, err := uuid.Parse(a)
	right, otherErr := uuid.Parse(b)
	return err == nil && otherErr == nil && left == right
}

func validName(value string, max int) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= max && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}
