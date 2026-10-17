package bindings

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

func TestVerificationReviewIsReadOnlyAndReadyDigestCreates(t *testing.T) {
	b, m, i, p := verificationBackendFixture(t)
	ctx := t.Context()
	capture := b.CaptureTask
	b.CaptureTask = func(context.Context, string, api.BindingVerificationPlanStep, time.Time) (*state.CreateAppTaskParams, error) {
		t.Error("review captured private task admission")
		return nil, state.ErrConflict
	}
	review, err := b.Review(ctx, api.ReviewBindingVerificationRequest{DeploymentID: i.VerificationDeploymentID})
	if err != nil || !review.Ready || review.ReviewDigest == "" || len(review.Plan) != 1 || review.Plan[0].Action != "probe" {
		t.Fatalf("review: %+v %v", review, err)
	}
	tasks, err := m.ListAppTasks(ctx, b.AccountID, b.AppID, 10, 0)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("review admitted work: %+v %v", tasks, err)
	}
	b.CaptureTask = capture
	request := api.CreateBindingVerificationRunRequest{ID: uuid.NewString(), DeploymentID: review.DeploymentID, ServiceTargets: review.ServiceTargets, ReviewDigest: review.ReviewDigest, TTLSeconds: 60}
	run, err := b.Create(ctx, request)
	if err != nil || run.ReviewDigest != review.ReviewDigest {
		t.Fatalf("review-to-create: %+v %v", run, err)
	}
	if _, err = b.Resume(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	p.Revision++
	request.ID = uuid.NewString()
	if _, err = b.Create(ctx, request); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale review created: %v", err)
	}
	request.ReviewDigest = strings.Repeat("b", 64)
	if _, err = b.Create(ctx, request); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("tampered digest created: %v", err)
	}
}
func TestVerificationReviewRejectsPartialAndReportsUnavailableTasks(t *testing.T) {
	b, _, i, _ := verificationBackendFixture(t)
	ctx := t.Context()
	i.Complete = false
	if _, err := b.Review(ctx, api.ReviewBindingVerificationRequest{DeploymentID: i.VerificationDeploymentID}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("partial inventory reviewed: %v", err)
	}
	i.Complete = true
	enabled := false
	b.TaskProbesEnabled = &enabled
	review, err := b.Review(ctx, api.ReviewBindingVerificationRequest{DeploymentID: i.VerificationDeploymentID})
	if err != nil || review.Ready || review.ReviewDigest != "" || len(review.Blockers) == 0 || review.Blockers[0].Code != "task_probes_unavailable" {
		t.Fatalf("feature blocker: %+v %v", review, err)
	}
}
func TestProjectVerificationReviewReadOnlyAndTamperFence(t *testing.T) {
	b, _, inventories := graphBackendFixture(t)
	ctx := t.Context()
	// Preparing selection remains separate from review; obtain its canonical
	// current manifest exactly as SDK users do through the prepare endpoint.
	deployments := map[string]string{}
	for name, i := range inventories {
		id, _ := uuid.Parse(i.VerificationDeploymentID)
		deployments[name] = id.String()
	}
	manifest, problem := PrepareProjectRelease(ctx, b.Catalog, b.AccountID, b.Project, b.Environment, api.PublishProjectReleaseSetRequest{Deployments: deployments, TTLSeconds: 60})
	if problem != nil {
		t.Fatal(problem)
	}
	input := api.ReviewProjectBindingVerificationRequest{Manifest: manifest, QueuePublish: false}
	original := b.Member
	b.Member = func(ctx context.Context, name, id string) (VerificationRunBackend, error) {
		member, err := original(ctx, name, id)
		member.CaptureTask = func(context.Context, string, api.BindingVerificationPlanStep, time.Time) (*state.CreateAppTaskParams, error) {
			t.Error("graph review captured private admission")
			return nil, state.ErrConflict
		}
		return member, err
	}
	review, err := b.Review(ctx, input)
	if err != nil || !review.Ready || review.ReviewDigest == "" || len(review.Members) != 2 || len(review.ReviewDigests) != 2 {
		t.Fatalf("graph review: %+v %v", review, err)
	}
	b.Member = original
	request := api.CreateProjectBindingVerificationRunRequest{ID: uuid.NewString(), Manifest: review.Manifest, ReviewDigest: review.ReviewDigest, ReviewDigests: review.ReviewDigests, TTLSeconds: 60, QueuePublish: review.QueuePublish, ObjectStorageWrite: review.ObjectStorageWrite}
	altered := request
	altered.QueuePublish = true
	if _, err = b.Create(ctx, altered); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed consent bypassed review: %v", err)
	}
	altered = request
	altered.Manifest.TTLSeconds++
	if _, err = b.Create(ctx, altered); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed manifest bypassed review: %v", err)
	}
	run, err := b.Create(ctx, request)
	if err != nil || run.ReviewDigest != review.ReviewDigest {
		t.Fatalf("graph review-to-create: %+v %v", run, err)
	}
	if _, err = b.Resume(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
}
