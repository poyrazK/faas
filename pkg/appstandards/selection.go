package appstandards

import (
	"fmt"
	"sort"

	"github.com/google/uuid"
)

// ApplicationOwnership is read from the stored app. Request labels, active
// API-key organization and the legacy creator account never select a layer.
type ApplicationOwnership struct{ ID, OrgID, ProjectID string }

type Assignment struct {
	ID               string `json:"id"`
	OrgID            string `json:"org_id"`
	Scope            string `json:"scope"`
	ScopeID          string `json:"scope_id"`
	StandardID       string `json:"standard_id"`
	AdmissionVersion int64  `json:"admission_version"`
}

// Adoption pins existing applications while an assignment admits new services
// at a newer version. A publisher cannot move these pins by adding a version.
type Adoption struct {
	AssignmentID string `json:"assignment_id"`
	Version      int64  `json:"version"`
}
type VersionKey struct {
	StandardID string
	Version    int64
}
type PublishedVersion struct {
	OrgID          string
	Definition     Definition
	DefinitionHash string
}
type Selection struct {
	Layers    []Layer
	Adoptions []Adoption
}

// SelectAdopted resolves an existing application's saved adoptions. Admission
// pointers govern newly created services; they cannot enroll an older service
// ahead of its approved batch. An inactive assignment can still have existing
// adoptions while its controlled removal is in progress. Storage supplies all
// retained assignment identities, including those disabled for new admissions.
func SelectAdopted(app ApplicationOwnership, assignments []Assignment, adopted []Adoption, versions map[VersionKey]PublishedVersion, limits Limits) (Selection, error) {
	pins := map[string]bool{}
	for _, pin := range adopted {
		if !standardIdentity(pin.AssignmentID) {
			return Selection{}, fmt.Errorf("invalid application adoption")
		}
		pins[canonicalIdentity(pin.AssignmentID)] = true
	}
	selected := []Assignment{}
	for _, assignment := range assignments {
		if _, err := assignmentMatches(app, assignment); err != nil {
			return Selection{}, err
		}
		if pins[canonicalIdentity(assignment.ID)] {
			selected = append(selected, assignment)
		}
	}
	return Select(app, selected, adopted, versions, limits)
}

// Select uses explicit assignment admission versions for new apps and saved
// pins for existing apps. Storage supplies active assignments and org-scoped
// immutable versions; selection still verifies identity, ownership and hashes.
func Select(app ApplicationOwnership, assignments []Assignment, adopted []Adoption, versions map[VersionKey]PublishedVersion, limits Limits) (Selection, error) {
	result := Selection{Layers: []Layer{}, Adoptions: []Adoption{}}
	if !standardIdentity(app.ID) || !standardIdentity(app.OrgID) || (app.ProjectID != "" && !standardIdentity(app.ProjectID)) {
		return result, fmt.Errorf("invalid persisted application ownership")
	}
	app.ID, app.OrgID = canonicalIdentity(app.ID), canonicalIdentity(app.OrgID)
	if app.ProjectID != "" {
		app.ProjectID = canonicalIdentity(app.ProjectID)
	}
	canonicalVersions := map[VersionKey]PublishedVersion{}
	for key, published := range versions {
		if !standardIdentity(key.StandardID) || key.Version < 1 {
			return result, fmt.Errorf("invalid available standard version")
		}
		key.StandardID = canonicalIdentity(key.StandardID)
		if _, exists := canonicalVersions[key]; exists {
			return result, fmt.Errorf("duplicate available standard version")
		}
		canonicalVersions[key] = published
	}
	pins := map[string]int64{}
	for _, pin := range adopted {
		if !standardIdentity(pin.AssignmentID) || pin.Version < 1 {
			return result, fmt.Errorf("invalid application adoption")
		}
		pin.AssignmentID = canonicalIdentity(pin.AssignmentID)
		if _, exists := pins[pin.AssignmentID]; exists {
			return result, fmt.Errorf("duplicate application adoption")
		}
		pins[pin.AssignmentID] = pin.Version
	}
	seenIDs, seenStandards := map[string]bool{}, map[string]bool{}
	for _, assignment := range assignments {
		matches, err := assignmentMatches(app, assignment)
		if err != nil {
			return result, err
		}
		if !matches {
			continue
		}
		assignment.ID, assignment.OrgID, assignment.ScopeID, assignment.StandardID = canonicalIdentity(assignment.ID), canonicalIdentity(assignment.OrgID), canonicalIdentity(assignment.ScopeID), canonicalIdentity(assignment.StandardID)
		key := assignment.Scope + "/" + assignment.StandardID
		if seenIDs[assignment.ID] || seenStandards[key] {
			return result, fmt.Errorf("duplicate active standard assignment")
		}
		seenIDs[assignment.ID], seenStandards[key] = true, true
		version := assignment.AdmissionVersion
		if pinned, ok := pins[assignment.ID]; ok {
			version = pinned
		}
		published, ok := canonicalVersions[VersionKey{StandardID: assignment.StandardID, Version: version}]
		if !ok || !sameIdentity(published.OrgID, app.OrgID) {
			return result, fmt.Errorf("selected standard version is missing or outside the application organization")
		}
		definition, hash, err := Normalize(published.Definition, limits)
		if err != nil {
			return result, err
		}
		if hash != published.DefinitionHash {
			return result, fmt.Errorf("selected standard definition hash mismatch")
		}
		result.Layers = append(result.Layers, Layer{AssignmentID: assignment.ID, ScopeID: assignment.ScopeID, StandardID: assignment.StandardID, Version: version, Scope: assignment.Scope, Definition: definition})
		result.Adoptions = append(result.Adoptions, Adoption{AssignmentID: assignment.ID, Version: version})
		if len(result.Layers) > limits.Layers {
			return Selection{}, fmt.Errorf("too many inherited standard layers")
		}
	}
	// Pins for assignments which no longer match ownership are intentionally
	// discarded. They cannot keep a former organization's requirements active.
	sort.Slice(result.Layers, func(i, j int) bool {
		a, b := result.Layers[i], result.Layers[j]
		if scopeRank(a.Scope) != scopeRank(b.Scope) {
			return scopeRank(a.Scope) < scopeRank(b.Scope)
		}
		if a.StandardID != b.StandardID {
			return a.StandardID < b.StandardID
		}
		return a.AssignmentID < b.AssignmentID
	})
	sort.Slice(result.Adoptions, func(i, j int) bool { return result.Adoptions[i].AssignmentID < result.Adoptions[j].AssignmentID })
	return result, nil
}

func assignmentMatches(app ApplicationOwnership, assignment Assignment) (bool, error) {
	if !standardIdentity(assignment.ID) || !standardIdentity(assignment.OrgID) || !standardIdentity(assignment.ScopeID) || !standardIdentity(assignment.StandardID) || assignment.AdmissionVersion < 1 || scopeRank(assignment.Scope) < 0 {
		return false, fmt.Errorf("invalid active standard assignment")
	}
	var matches bool
	switch assignment.Scope {
	case "organization":
		if !sameIdentity(assignment.ScopeID, assignment.OrgID) {
			return false, fmt.Errorf("organization assignment scope differs from its owner")
		}
		matches = sameIdentity(assignment.ScopeID, app.OrgID)
	case "project":
		matches = app.ProjectID != "" && sameIdentity(assignment.ScopeID, app.ProjectID)
	case "application":
		matches = sameIdentity(assignment.ScopeID, app.ID)
	}
	if matches && !sameIdentity(assignment.OrgID, app.OrgID) {
		return false, fmt.Errorf("assigned scope belongs to another organization")
	}
	return matches, nil
}

func standardIdentity(raw string) bool {
	id, err := uuid.Parse(raw)
	return err == nil && id != uuid.Nil
}

func canonicalIdentity(raw string) string { id, _ := uuid.Parse(raw); return id.String() }
func sameIdentity(a, b string) bool {
	first, err := uuid.Parse(a)
	if err != nil || first == uuid.Nil {
		return false
	}
	second, err := uuid.Parse(b)
	return err == nil && first == second
}
