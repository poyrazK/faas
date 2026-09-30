package appstandards

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// ApprovalInputs is built by trusted storage after loading the complete scope.
// ScopeSnapshotHash binds membership, current assignments, immutable resources,
// exceptions, entitlements and artifact evidence. It is not a caller-supplied
// substitute for those reads. SnapshotHash binds the corresponding app inputs.
// Hashes keep sealed credentials and artifact bodies out of the approval token.
type ApprovalInputs struct {
	PlanID            string                     `json:"plan_id"`
	OrgID             string                     `json:"org_id"`
	CreatedBy         string                     `json:"created_by"`
	ScopeSnapshotHash string                     `json:"scope_snapshot_hash"`
	Assignment        Assignment                 `json:"assignment"`
	ExpectedRevision  int64                      `json:"expected_revision"`
	Active            bool                       `json:"active"`
	BatchSize         int                        `json:"batch_size"`
	Applications      []ApplicationApprovalInput `json:"applications"`
}

type ApplicationApprovalInput struct {
	AppID           string     `json:"app_id"`
	OrgID           string     `json:"org_id"`
	ProjectID       string     `json:"project_id,omitempty"`
	DesiredRevision int64      `json:"desired_revision"`
	SnapshotHash    string     `json:"snapshot_hash"`
	BeforeAdoptions []Adoption `json:"before_adoptions"`
	AfterAdoptions  []Adoption `json:"after_adoptions"`
	EffectiveHash   string     `json:"effective_hash"`
}

// HashApproval returns the same digest when database ordering or UUID spelling
// changes, but a different digest for any reviewed input change. Storage must
// recompute all snapshot hashes under its apply locks before comparing this
// digest; hashing a saved preview again cannot establish freshness.
func HashApproval(input ApprovalInputs) (string, error) {
	for _, id := range []string{input.PlanID, input.OrgID, input.CreatedBy} {
		if !standardIdentity(id) {
			return "", fmt.Errorf("invalid approval identity")
		}
	}
	if !approvalDigest(input.ScopeSnapshotHash) || input.ExpectedRevision < 0 || input.BatchSize < 1 {
		return "", fmt.Errorf("invalid approval snapshot, revision or batch size")
	}
	input.PlanID, input.OrgID, input.CreatedBy = canonicalIdentity(input.PlanID), canonicalIdentity(input.OrgID), canonicalIdentity(input.CreatedBy)
	a := input.Assignment
	if _, err := assignmentMatches(ApplicationOwnership{ID: a.ID, OrgID: input.OrgID}, a); err != nil {
		return "", err
	}
	if !sameIdentity(a.OrgID, input.OrgID) {
		return "", fmt.Errorf("approval assignment belongs to another organization")
	}
	a.ID, a.OrgID, a.ScopeID, a.StandardID = canonicalIdentity(a.ID), canonicalIdentity(a.OrgID), canonicalIdentity(a.ScopeID), canonicalIdentity(a.StandardID)
	input.Assignment = a
	input.Applications = append([]ApplicationApprovalInput{}, input.Applications...)
	seen := map[string]bool{}
	for i := range input.Applications {
		app := &input.Applications[i]
		if !standardIdentity(app.AppID) || !sameIdentity(app.OrgID, input.OrgID) || (app.ProjectID != "" && !standardIdentity(app.ProjectID)) || app.DesiredRevision < 1 || !approvalDigest(app.SnapshotHash) || !approvalDigest(app.EffectiveHash) {
			return "", fmt.Errorf("invalid application approval inputs")
		}
		app.AppID, app.OrgID = canonicalIdentity(app.AppID), canonicalIdentity(app.OrgID)
		if app.ProjectID != "" {
			app.ProjectID = canonicalIdentity(app.ProjectID)
		}
		matches, err := assignmentMatches(ApplicationOwnership{ID: app.AppID, OrgID: app.OrgID, ProjectID: app.ProjectID}, a)
		if err != nil || !matches || seen[app.AppID] {
			return "", fmt.Errorf("approval contains a duplicate or out-of-scope application")
		}
		seen[app.AppID] = true
		app.BeforeAdoptions, err = canonicalApprovalAdoptions(app.BeforeAdoptions)
		if err != nil {
			return "", err
		}
		app.AfterAdoptions, err = canonicalApprovalAdoptions(app.AfterAdoptions)
		if err != nil {
			return "", err
		}
		adopted := false
		for _, pin := range app.AfterAdoptions {
			if pin.AssignmentID == a.ID {
				if !input.Active || pin.Version != a.AdmissionVersion {
					return "", fmt.Errorf("approval adoption differs from its assignment change")
				}
				adopted = true
			}
		}
		if input.Active && !adopted {
			return "", fmt.Errorf("approval omits the active assignment adoption")
		}
	}
	slices.SortFunc(input.Applications, func(a, b ApplicationApprovalInput) int { return strings.Compare(a.AppID, b.AppID) })
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte("gregale.application-standard.approval.v1\x00"), raw...))
	return hex.EncodeToString(digest[:]), nil
}

func canonicalApprovalAdoptions(input []Adoption) ([]Adoption, error) {
	output := append([]Adoption{}, input...)
	seen := map[string]bool{}
	for i := range output {
		pin := &output[i]
		if !standardIdentity(pin.AssignmentID) || pin.Version < 1 {
			return nil, fmt.Errorf("invalid approval adoption")
		}
		pin.AssignmentID = canonicalIdentity(pin.AssignmentID)
		if seen[pin.AssignmentID] {
			return nil, fmt.Errorf("duplicate approval adoption")
		}
		seen[pin.AssignmentID] = true
	}
	slices.SortFunc(output, func(a, b Adoption) int { return strings.Compare(a.AssignmentID, b.AssignmentID) })
	return output, nil
}

func approvalDigest(raw string) bool {
	if len(raw) != sha256.Size*2 {
		return false
	}
	for _, character := range raw {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
