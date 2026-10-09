package api

import "time"

// A saved investigation stores selections and commentary, never profile samples.
type ProfileInvestigationInput struct {
	Title        string           `json:"title"`
	Findings     string           `json:"findings"`
	Notes        string           `json:"notes"`
	Baseline     ProfileQuery     `json:"baseline"`
	Candidate    ProfileQuery     `json:"candidate"`
	SelectedPath *ProfileCallPath `json:"selected_path,omitempty"`
}

type ProfileCallPath struct {
	View   string                 `json:"view"`
	Frames []ProfileCallPathFrame `json:"frames"`
}

type ProfileCallPathFrame struct {
	Name            string                 `json:"name"`
	File            string                 `json:"file,omitempty"`
	Line            int64                  `json:"line,omitempty"`
	BaselinePath    string                 `json:"baseline_path,omitempty"`
	BaselineLine    int64                  `json:"baseline_line,omitempty"`
	CandidatePath   string                 `json:"candidate_path,omitempty"`
	CandidateLine   int64                  `json:"candidate_line,omitempty"`
	BaselineSource  *ProfileSourceLocation `json:"baseline_source,omitempty"`
	CandidateSource *ProfileSourceLocation `json:"candidate_source,omitempty"`
}

type SaveProfileInvestigationRequest struct {
	// InitialAssessment is resolved by the server, never accepted from JSON.
	InitialAssessment *ProfileRegressionAssessment `json:"-"`
	ExpectedRevision  *int64                       `json:"expected_revision"`
	Investigation     ProfileInvestigationInput    `json:"investigation"`
}

type ProfileInvestigation struct {
	ID            string                       `json:"id"`
	AppID         string                       `json:"app_id"`
	Revision      int64                        `json:"revision"`
	Investigation ProfileInvestigationInput    `json:"investigation"`
	CreatedAt     time.Time                    `json:"created_at"`
	UpdatedAt     time.Time                    `json:"updated_at"`
	Assessment    *ProfileRegressionAssessment `json:"assessment,omitempty"`
}

// Retained means eligible for querying, not proof that samples exist.
type ProfileInvestigationWindowStatus struct {
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type ProfileInvestigationResponse struct {
	Saved           ProfileInvestigation             `json:"saved"`
	URL             string                           `json:"url"`
	BaselineStatus  ProfileInvestigationWindowStatus `json:"baseline_status"`
	CandidateStatus ProfileInvestigationWindowStatus `json:"candidate_status"`
}

type ListProfileInvestigationsResponse struct {
	Investigations []ProfileInvestigationResponse `json:"investigations"`
}
