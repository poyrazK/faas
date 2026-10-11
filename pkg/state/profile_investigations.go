package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrProfileInvestigationRevision = errors.New("saved profiling investigation revision is stale")
var ErrProfileInvestigationQuota = errors.New("saved profiling investigation limit reached")

type ProfileInvestigationStore interface {
	SaveProfileInvestigation(context.Context, string, string, string, api.SaveProfileInvestigationRequest) (api.ProfileInvestigation, error)
	GetProfileInvestigation(context.Context, string, string, string) (api.ProfileInvestigation, error)
	ListProfileInvestigations(context.Context, string, string) ([]api.ProfileInvestigation, error)
	DeleteProfileInvestigation(context.Context, string, string, string, int64) error
	SaveProfileRegressionAssessment(context.Context, string, string, string, int64, api.ProfileRegressionAssessment) (api.ProfileInvestigation, error)
}

var (
	_ ProfileInvestigationStore = (*MemStore)(nil)
	_ ProfileInvestigationStore = (*PgStore)(nil)
)

func ValidateProfileInvestigation(req api.SaveProfileInvestigationRequest) error {
	return validateProfileInvestigationAt(req, time.Now())
}

func validateProfileInvestigationAt(req api.SaveProfileInvestigationRequest, now time.Time) error {
	if req.ExpectedRevision == nil || *req.ExpectedRevision < 0 || *req.ExpectedRevision >= api.ProfileInvestigationMaxRevision {
		return fmt.Errorf("expected_revision must be explicitly supplied; use 0 to create an investigation")
	}
	in := req.Investigation
	if a := req.InitialAssessment; a != nil {
		if a.InvestigationRevision != *req.ExpectedRevision+1 || a.CheckedAt.IsZero() || !sameInvestigationSelection(a.Baseline, in.Baseline) || !sameInvestigationSelection(a.Candidate, in.Candidate) {
			return errors.New("initial assessment does not match the investigation")
		}
		if err := validateProfileCanaryAssessment(*a); err != nil {
			return err
		}
	}
	if strings.TrimSpace(in.Title) == "" || !investigationText(in.Title, api.ProfileInvestigationMaxTitleBytes) || !investigationText(in.Findings, api.ProfileInvestigationMaxTextBytes) || !investigationText(in.Notes, api.ProfileInvestigationMaxTextBytes) {
		return fmt.Errorf("title is required (up to %d bytes); findings and notes allow %d bytes each", api.ProfileInvestigationMaxTitleBytes, api.ProfileInvestigationMaxTextBytes)
	}
	for _, q := range []api.ProfileQuery{in.Baseline, in.Candidate} {
		if !api.ValidProfileRoute(q.Route) {
			return errors.New("invalid profiling route")
		}
		id, err := uuid.Parse(q.DeploymentID)
		if err != nil || id.String() != q.DeploymentID || q.Start.IsZero() || !q.End.After(q.Start) || q.End.After(now.Add(time.Second)) || len(q.Runtime) == 0 || !investigationText(q.Runtime, api.ProfileMaxRuntimeBytes) {
			return errors.New("each selection requires a canonical deployment UUID, runtime and valid past time window")
		}
	}
	if in.Baseline.Runtime != in.Candidate.Runtime || in.Baseline.Route != in.Candidate.Route {
		return errors.New("baseline and candidate runtimes must match")
	}
	if p := in.SelectedPath; p != nil {
		if p.View != "candidate" && p.View != "comparison" || len(p.Frames) == 0 || len(p.Frames) > api.ProfileMaxStackDepth+1 {
			return errors.New("selected_path needs a candidate or comparison view and a bounded complete caller path")
		}
		size := 0
		for _, frame := range p.Frames {
			if !investigationText(frame.Name, api.ProfileMaxSymbolBytes) || !investigationText(frame.File, api.ProfileMaxSymbolBytes) || frame.Line < 0 ||
				frame.BaselinePath != "" || frame.BaselineLine != 0 || frame.CandidatePath != "" || frame.CandidateLine != 0 ||
				frame.BaselineSource != nil || frame.CandidateSource != nil {
				return errors.New("invalid selected call-path frame")
			}
			size += len(frame.Name) + len(frame.File)
		}
		if size > api.ProfileInvestigationMaxPathBytes {
			return errors.New("selected call path exceeds the symbol budget")
		}
	}
	body, err := json.Marshal(in)
	if err != nil || len(body) > api.ProfileInvestigationMaxBytes {
		return errors.New("investigation exceeds the storage budget")
	}
	return nil
}

func investigationText(value string, maxBytes int) bool {
	return len(value) <= maxBytes && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func copyProfileInvestigation(in api.ProfileInvestigation) api.ProfileInvestigation {
	if in.Investigation.SelectedPath != nil {
		path := *in.Investigation.SelectedPath
		path.Frames = append([]api.ProfileCallPathFrame(nil), path.Frames...)
		in.Investigation.SelectedPath = &path
	}
	if in.Assessment != nil {
		// Assessments are validated JSON values before storage.
		body, _ := json.Marshal(in.Assessment)
		var assessment api.ProfileRegressionAssessment
		_ = json.Unmarshal(body, &assessment)
		in.Assessment = &assessment
	}
	return in
}

func validateProfileAssessment(current api.ProfileInvestigation, revision int64, a api.ProfileRegressionAssessment) ([]byte, error) {
	if err := validateProfileAttribution(a.Attribution); err != nil {
		return nil, err
	}
	if err := validateProfileRouteChecks(a.Options, a.RouteChecks); err != nil {
		return nil, err
	}
	if err := validateProfileMixSnapshot(a.RequestMix, a.Baseline, a.Candidate); err != nil {
		return nil, err
	}
	if current.Revision != revision || revision < 1 || revision >= api.ProfileInvestigationMaxRevision {
		return nil, ErrProfileInvestigationRevision
	}
	if a.InvestigationRevision != revision+1 || a.CheckedAt.IsZero() || !sameInvestigationSelection(a.Baseline, current.Investigation.Baseline) || !sameInvestigationSelection(a.Candidate, current.Investigation.Candidate) {
		return nil, errors.New("assessment does not match the saved investigation")
	}
	if a.Status != "regressed" && a.Status != "no_regression_detected" && a.Status != "inconclusive" {
		return nil, errors.New("invalid assessment status")
	}
	body, err := json.Marshal(a)
	if err != nil || len(body) > api.ProfileRegressionMaxAssessmentBytes || len(a.Evidence) > api.ProfileRegressionMaxEvidence {
		return nil, errors.New("assessment exceeds the storage budget")
	}
	return body, nil
}
