// cmd/gregale/receipt.go — `DeployReceipt` DTO emitted by
// `gregale deploy --json` (issue #1182 §P1 follow-up to PR #1187).
//
// The receipt embeds api.DeploymentResponse so existing consumers that
// unmarshal `--json` output into api.DeploymentResponse keep working.
// encoding/json promotes the embedded struct's fields to top level,
// and an Unmarshal target that doesn't list the extra keys simply
// ignores them (see TestCmdDeploy_JSON_SkipsStream extension at
// cli_test.go:760).
//
// Receipt-only fields:
//   - app_url:        customer-facing canonical URL resolved by the API,
//                     with the CLI platform URL as a fallback.
//                     Empty when the slug is empty.
//   - commit_sha:     HEAD SHA from zeroConfigProvenance.SHA. Empty on
//                     non-git cwd-auto-pack, image, and source-ref
//                     paths (no git detection ran).
//   - dirty:          working-tree-is-dirty flag from provenance.
//                     false elsewhere (omitted vs explicit false —
//                     the JSON tag is `omitempty` so a non-git
//                     deploy renders no `dirty` key).
//   - source_sha256:  sha256 hex digest of the tarball bytes shipped
//                     to the server. Empty on image (no source
//                     bytes — dep.ImageDigest carries the OCI digest
//                     instead) and source-ref (server pulls the
//                     tarball, CLI never sees bytes) paths.

package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/simpleapp"
)

// DeployReleaseSummary is the compact release-to-release context attached to
// a waited deploy receipt. The API's full DeploymentSummaryResponse remains
// available from `gregale deployment summary`; this shape keeps the deploy
// receipt small while carrying the fields automation needs immediately after
// a successful release.
type DeployReleaseSummary struct {
	PreviousDeploymentID string                 `json:"previous_deployment_id,omitempty"`
	Changes              []api.DeploymentChange `json:"changes"`
	RollbackTargetID     string                 `json:"rollback_target_id,omitempty"`
	RollbackCommand      string                 `json:"rollback_command,omitempty"`
}

// SimpleAppReceiptPlan is the non-secret, effective stateless application
// contract attached to deploy receipts. It deliberately excludes the
// customer's source, environment, and credentials while preserving the
// runtime choices automation needs to understand after a deploy.
type SimpleAppReceiptPlan struct {
	ResourceProfile string `json:"resource_profile"`
	MemoryMB        int    `json:"memory_mb,omitempty"`
	CPUMillicores   int    `json:"cpu_millicores,omitempty"`
	Port            int    `json:"port"`
	HealthPath      string `json:"health_path"`
	ExecutionMode   string `json:"execution_mode"`
	ScaleToZero     bool   `json:"scale_to_zero"`
	LocalStorage    string `json:"local_storage"`
	DurableState    string `json:"durable_state"`
}

// DeployReceipt is the `gregale deploy --json` wire envelope. See
// cmd/gregale/receipt.go header comment for field provenance.
type DeployReceipt struct {
	api.DeploymentResponse
	AppURL         string                `json:"app_url,omitempty"`
	CommitSHA      string                `json:"commit_sha,omitempty"`
	Dirty          bool                  `json:"dirty,omitempty"`
	SourceSHA256   string                `json:"source_sha256,omitempty"`
	TimedOut       bool                  `json:"timed_out,omitempty"`
	ResumeCommand  string                `json:"resume_command,omitempty"`
	ReleaseSummary *DeployReleaseSummary `json:"release_summary,omitempty"`
	SimpleAppPlan  *SimpleAppReceiptPlan `json:"simple_app_plan,omitempty"`
}

// newDeployReceipt builds a DeployReceipt from the post-deploy
// DeploymentResponse plus optional zero-config provenance and the
// optional source-bytes SHA-256. nil-safe: a zero-value dep gives
// the zero DeploymentResponse on the wire; a nil prov leaves
// commit_sha and dirty at their zero values (matches the "no git
// detection ran" image / source-ref / non-git fallback paths).
//
// appURL is resolved from the app response when available, with the
// CLI-known slug as a fallback; the receipt deliberately does NOT use
// dep.AppID because the wire's app_id is the 32-char hex PK
// (per openapi.yaml:12053, `pattern: '^[a-f0-9]{32}$'`), and the
// gateway routes on slug — a hex-keyed URL never resolves. When
// appURL is empty (CLI failed to resolve a slug), the omitempty
// tag on AppURL drops the key so consumers don't see a malformed
// `https://.gregale.dev` string.
func newDeployReceipt(dep api.DeploymentResponse, prov *zeroConfigProvenance, appURL, sourceSHA256 string, simplePlans ...*simpleapp.Plan) *DeployReceipt {
	r := &DeployReceipt{
		DeploymentResponse: dep,
		SourceSHA256:       sourceSHA256,
	}
	if appURL != "" {
		r.AppURL = appURL
	}
	if prov != nil {
		r.CommitSHA = prov.SHA
		r.Dirty = prov.Dirty
	}
	if len(simplePlans) > 0 && simplePlans[0] != nil {
		plan := simplePlans[0]
		r.SimpleAppPlan = &SimpleAppReceiptPlan{
			ResourceProfile: plan.ResourceProfile,
			MemoryMB:        plan.MemoryMB,
			CPUMillicores:   plan.CPUMillicores,
			Port:            plan.Port,
			HealthPath:      plan.HealthPath,
			ExecutionMode:   plan.ExecutionMode,
			ScaleToZero:     plan.ScaleToZero,
			LocalStorage:    plan.LocalStorage,
			DurableState:    plan.DurableState,
		}
	}
	return r
}

func newDeployReleaseSummary(summary api.DeploymentSummaryResponse, appSlug string) *DeployReleaseSummary {
	release := &DeployReleaseSummary{
		Changes: append([]api.DeploymentChange(nil), summary.Changes...),
	}
	if release.Changes == nil {
		release.Changes = []api.DeploymentChange{}
	}
	if summary.Previous != nil {
		release.PreviousDeploymentID = summary.Previous.ID
	}
	if summary.RollbackTargetID != "" {
		release.RollbackTargetID = summary.RollbackTargetID
		if appSlug != "" {
			release.RollbackCommand = "gregale rollback " + appSlug + " --to " + summary.RollbackTargetID
		}
	}
	return release
}
