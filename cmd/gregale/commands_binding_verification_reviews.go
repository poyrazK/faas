package main

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"maps"
	"reflect"
	"time"
)

func fetchBindingVerificationReview(ctx context.Context, client *api.Client, app, ref string, targets map[string]string) (api.BindingVerificationReview, error) {
	id, err := resolveBindingDeployment(ctx, client, app, ref)
	if err != nil {
		return api.BindingVerificationReview{}, err
	}
	review, err := client.ReviewBindingVerification(ctx, app, api.ReviewBindingVerificationRequest{DeploymentID: id, ServiceTargets: targets})
	if err != nil {
		return review, err
	}
	if err = validateBindingVerificationReview(review, app, id, targets); err != nil {
		return review, err
	}
	return review, nil
}
func validateBindingVerificationReview(r api.BindingVerificationReview, app, id string, targets map[string]string) error {
	if r.App != app || r.Scope != r.Policy.Scope || r.DeploymentID != r.Inventory.VerificationDeploymentID || r.ReviewedAt.IsZero() || r.ReviewedAt.After(time.Now()) || !maps.Equal(r.ServiceTargets, targets) || !validBindingPolicyServiceTargets(r.Inventory, targets) || !validBindingPolicyConfigurationStamp(r.Inventory) || !validReleasePolicyCheckSelection(r.Inventory, bindingcheck.Policy{App: app, Scope: r.Scope, DeploymentID: r.DeploymentID}) || id != "" && !sameBindingDeployment(id, r.DeploymentID) || len(r.Plan) > api.BindingVerificationRunMaxSteps {
		return errors.New("server did not confirm the complete verification selection")
	}
	if err := validateReleasePolicyCheckTarget(r.Policy, r.Policy.AppID, r.Scope); err != nil {
		return err
	}
	if r.Ready {
		decoded, err := hex.DecodeString(r.ReviewDigest)
		if err != nil || len(decoded) != 32 || len(r.Blockers) != 0 {
			return errors.New("server did not confirm a ready review digest")
		}
	}
	return nil
}
func reportFromBindingVerificationReview(review api.BindingVerificationReview, consent bool) (bindingPolicyVerificationReport, int) {
	report := bindingPolicyVerificationReport{App: review.App, DeploymentID: review.DeploymentID, Scope: review.Scope, DryRun: true, WriteConsent: consent, ReviewDigest: review.ReviewDigest, PlanReady: review.Ready, SnapshotConfirmed: true, Status: "planned", ReleasePolicy: &review.Policy, ConfigChangedAt: review.Inventory.RuntimeFreshness.ConfigChangedAt, Plan: review.Plan, PlanBlockers: review.Blockers, Probes: []bindingPolicyProbeResult{}, WriteConsentRequired: review.ObjectStorageWriteRequired, QueuePublishConsentRequired: review.QueuePublishRequired}
	if review.ObservedCheck != nil {
		observed := bindingcheck.Report(*review.ObservedCheck)
		report.ObservedCheck = &observed
	}
	if !review.Ready {
		return bindingPolicyVerificationFailed(report, "plan_invalid", errors.New("the server review has blockers; inspect plan_blockers"))
	}
	return report, 0
}
func executeServerBindingReview(ctx context.Context, client *api.Client, app, ref string, consent bool, targets ...map[string]string) (bindingPolicyVerificationReport, int) {
	var selected map[string]string
	if len(targets) > 0 {
		selected = targets[0]
	}
	review, err := fetchBindingVerificationReview(ctx, client, app, ref, selected)
	if err != nil {
		return bindingPolicyVerificationFailed(bindingPolicyVerificationReport{App: app, DryRun: true, Status: "blocked", Plan: []bindingPolicyProbePlan{}, Probes: []bindingPolicyProbeResult{}}, "review_unavailable", err)
	}
	if revision, ok := ctx.Value(bindingPolicyExpectedRevisionKey{}).(int64); ok && revision != review.Policy.Revision {
		return bindingPolicyVerificationFailed(bindingPolicyVerificationReport{App: app, DryRun: true, Status: "blocked"}, "release_policy_changed", errors.New("prepared graph policy changed"))
	}
	return reportFromBindingVerificationReview(review, consent)
}
func fetchProjectVerificationReview(ctx context.Context, client *api.Client, manifest api.CheckedProjectReleaseManifest, write, queue bool) (api.ProjectBindingVerificationReview, error) {
	review, err := client.ReviewProjectBindingVerification(ctx, manifest.Project, manifest.Environment, api.ReviewProjectBindingVerificationRequest{Manifest: manifest, ObjectStorageWrite: write, QueuePublish: queue})
	if err != nil {
		return review, err
	}
	if review.Project != manifest.Project || review.Environment != manifest.Environment || review.ReviewedAt.IsZero() || review.ReviewedAt.After(time.Now()) || review.ObjectStorageWrite != write || review.QueuePublish != queue || len(review.Members) != len(manifest.Deployments) || !reflect.DeepEqual(review.Manifest, manifest) {
		return review, errors.New("server did not confirm the original graph review")
	}
	for name, member := range review.Members {
		if err = validateBindingVerificationReview(member, name, manifest.Deployments[name], manifest.ServiceTargets[name]); err != nil {
			return review, err
		}
		if member.Policy.Revision != manifest.ExpectedPolicyRevisions[name] || review.ReviewDigests[name] != member.ReviewDigest || review.Ready && !member.Ready {
			return review, errors.New("server did not confirm every graph member review")
		}
	}
	if review.Ready {
		decoded, err := hex.DecodeString(review.ReviewDigest)
		if err != nil || len(decoded) != 32 || len(review.Blockers) != 0 {
			return review, errors.New("server did not confirm a ready graph digest")
		}
	}
	return review, nil
}
func previewProjectVerification(ctx context.Context, client *api.Client, manifest api.CheckedProjectReleaseManifest, write, queue bool) int {
	review, err := fetchProjectVerificationReview(ctx, client, manifest, write, queue)
	if err != nil {
		return printErr("Could not review graph verification", err)
	}
	reports := map[string]bindingPolicyVerificationReport{}
	code := 0
	for name, member := range review.Members {
		report, result := reportFromBindingVerificationReview(member, write)
		reports[name] = report
		if result != 0 {
			code = result
		}
	}
	return renderCheckedReleaseVerification(manifest, reports, true, code, review)
}
