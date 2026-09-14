// cmd/apid/handlers_annotations.go — shared validation + audit
// helpers for the deployment annotation surface (issue #977 /
// ADR-116). Three call sites (handleSourceTarballDeploy,
// handleSourceRefDeploy, the JSON createDeployment path) all need
// to:
//
//  1. Lift annotation fields off their wire (multipart form value
//     or JSON body).
//  2. Validate them against the DB CHECKs (length cap, closed-set
//     tag, pr_number > 0) and surface RFC 7807 problems on
//     rejection.
//  3. Stamp them onto the deployment row via apidsource.Enqueue.
//  4. Mirror them into the audit data{} map (only when present —
//     pre-feature rows stay byte-identical at the JSON layer).
//
// Centralising the seam here keeps the three handlers from drifting
// on validation rules + audit-key naming. The DB CHECK at
// migrations/00346_deployments_annotation.sql is the source of
// truth for length / closed-set / >0; this file mirrors them so
// a customer gets a clean 422 instead of a 500 from the constraint
// trip.

package main

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// annotationForm is the canonical annotation shape after wire
// deserialisation. Same fields as pkg/api.DeployAnnotations but
// lives in cmd/apid so the apid-side handlers don't need to import
// the SDK DTO (which is the public surface). Zero value means "no
// annotation"; nil/empty form values → zero here → NULL on the row.
type annotationForm struct {
	Reason     string
	Tag        string
	DeployedBy string
	PRNumber   int
}

// annotationTags is the canonical closed-set vocabulary for the
// annotation tag field (issue #977 / ADR-116). Mirrors the DB CHECK
// at migrations/00346_deployments_annotation.sql. Hoisted into a
// package-level constant so the validator and the rejection-message
// builder share the same source of truth (goconst flags the inline
// literals otherwise).
//
// Source of truth: migrations/00346_deployments_annotation.sql.
// The CLI's DeploymentAnnotationTags list (cmd/gregale/cmd_deploy_
// annotations.go) is the same vocabulary; drift is caught by the
// CLI's TestDeploymentAnnotationTags_MirrorsDB.
var annotationTags = []string{
	"incident_recovery",
	"hotfix",
	"scheduled_maintenance",
	"compliance_hold",
	"partner_request",
}

// annotationTagsCSV is the comma-joined form of annotationTags, used
// in the ValidDeploymentAnnotationTag rejection message so the
// detail line is generated from the same source as the validator.
var annotationTagsCSV = strings.Join(annotationTags, ", ")

// ValidDeploymentAnnotationTag returns true iff s is one of the
// canonical closed-set values (mirrors the DB CHECK). Empty is
// allowed (== no tag).
func ValidDeploymentAnnotationTag(s string) bool {
	if s == "" {
		return true
	}
	for _, t := range annotationTags {
		if s == t {
			return true
		}
	}
	return false
}

// strFromPtr returns *p when p != nil, "" otherwise. Used by the
// JSON-wire path (CreateDeploymentRequest) to lift *string fields
// onto annotationForm's value-type fields without nil checks at
// every call site.
func strFromPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// intFromPtr is the int counterpart of strFromPtr.
func intFromPtr(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// annotationFormFromRequest reads the four annotation form fields
// from a multipart request (the source-tarball + multipart
// createDeployment paths). r.FormValue returns "" on missing;
// strconv.Atoi on a missing/empty pr_number returns 0. Both are
// mapped to "no annotation" downstream.
func annotationFormFromRequest(r *http.Request) annotationForm {
	prNum, _ := strconv.Atoi(r.FormValue("pr_number"))
	return annotationForm{
		Reason:     r.FormValue("reason"),
		Tag:        r.FormValue("tag"),
		DeployedBy: r.FormValue("deployed_by"),
		PRNumber:   prNum,
	}
}

// annotationFromRequest lifts annotations off the JSON-wire source-
// ref request. The DTO uses value-types (string / int) so a missing
// wire field reads as the zero value ("" / 0), which is treated
// identically to "absent" — the pgstore collapses both to NULL.
func annotationFromRequest(req api.SourceRefDeployRequest) annotationForm {
	return annotationForm{
		Reason:     req.Reason,
		Tag:        req.Tag,
		DeployedBy: req.DeployedBy,
		PRNumber:   req.PRNumber,
	}
}

// validateAnnotationForm checks the annotationForm against the DB
// CHECKs at migrations/00346_deployments_annotation.sql. Returns
// an RFC 7807 problem on rejection, nil on success. Empty
// annotationForm (no fields set) always validates — callers that
// don't care about annotations stay on the pre-feature wire.
func validateAnnotationForm(ann annotationForm) *api.Problem {
	if utf8.RuneCountInString(ann.Reason) > 280 {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid reason",
			"reason must be ≤280 characters")
	}
	if !ValidDeploymentAnnotationTag(ann.Tag) {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid tag",
			"tag must be one of: "+annotationTagsCSV)
	}
	if ann.PRNumber < 0 {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid pr_number",
			"pr_number must be a positive integer (or absent)")
	}
	return nil
}

// mergeAnnotationAudit adds the 4 annotation keys to a data map only
// when non-zero. Pre-feature rows (zero annotationForm) leave the
// map byte-identical to the pre-#977 wire shape; post-feature rows
// gain 4 new keys. The audit_log JSON column tolerates extra keys
// without a schema change — `events.data jsonb` is open by design.
func mergeAnnotationAudit(data map[string]any, ann annotationForm) {
	if ann.Reason != "" {
		data["reason"] = ann.Reason
	}
	if ann.Tag != "" {
		data["tag"] = ann.Tag
	}
	if ann.DeployedBy != "" {
		data["deployed_by"] = ann.DeployedBy
	}
	if ann.PRNumber > 0 {
		data["pr_number"] = ann.PRNumber
	}
}
