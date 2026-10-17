package proposal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/promotion"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func fixture(t *testing.T, provider string) (Request, []byte) {
	t.Helper()
	image := "registry.example/api@sha256:"
	raw := []byte("# production configuration stays here\napi_version: gregale.environment/v1\nproject: shop\nenvironment: production\nworkloads:\n  api:\n    app: shop-api\n    source:\n      kind: image\n      image: " + image + strings.Repeat("1", 64) + "\n    variables:\n      MODE: production-private # keep comment\n    secret_refs:\n      TOKEN: secret:PRODUCTION_TOKEN\n")
	// Keep the fixture aligned with the canonical API version.
	raw = bytes.ReplaceAll(raw, []byte("gregale.environment/v1"), []byte(environmentsync.APIVersion))
	definition, err := gregalemanifest.ParseEnvironment(raw)
	if err != nil {
		t.Fatal(err)
	}
	base, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	before := *definition.Workloads["api"].Source
	after := before
	after.Image = image + strings.Repeat("2", 64)
	workload := definition.Workloads["api"]
	workload.Source = &after
	definition.Workloads["api"] = workload
	proposed, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	plan := api.PreviewEnvironmentGitPromotionResponse{Project: "shop", FromEnvironment: "staging", ToEnvironment: "production", ReadOnly: true, ValuesRedacted: true, RequiresTargetQualification: true,
		Origin:  api.EnvironmentGitPromotionOrigin{SourceID: "staging-source", RevisionID: "revision", CommitSHA: strings.Repeat("b", 40), DefinitionDigest: strings.Repeat("b", 64), RepositoryID: 42, Provider: provider, ReleaseSetID: "2ba0aa40-2362-4c0e-aeb1-6381666bb514"},
		Target:  api.EnvironmentGitPromotionTarget{SourceID: "production-source", Generation: 2, IntentVersion: 3, CommitSHA: sha, DefinitionDigest: base.Digest, ProposedDigest: proposed.Digest, ManifestPath: "environments/production.yaml"},
		Changes: []api.EnvironmentGitPromotionChange{{Resource: "workload/api", Before: before, After: after}}, BlockingReasons: []string{},
	}
	plan, err = promotion.Finish(plan, api.EnvironmentGitOpsPlan{Manager: plan.Target.SourceID, Revision: "candidate:" + sha, CommitSHA: sha, Generation: 2, ObservedVersion: 3, DesiredDigest: proposed.Digest, Hash: strings.Repeat("e", 64)})
	if err != nil {
		t.Fatal(err)
	}
	installation := int64(12)
	if provider == "gitlab" {
		installation = 0
	}
	source := api.EnvironmentGitSource{ID: plan.Target.SourceID, Generation: 2, IntentVersion: 3, EnvironmentSlug: "production", Spec: api.EnvironmentGitSourceSpec{Provider: provider, Repository: "shop/platform", RepositoryID: 42, InstallationID: installation, Ref: "refs/heads/main", ManifestPath: plan.Target.ManifestPath, Mode: "report", ApprovalPolicy: "manual"}}
	request := Request{Plan: plan, Source: source, ExpectedPlanHash: plan.PlanHash}
	if err := request.validate(); err != nil {
		t.Fatal(err)
	}
	return request, raw
}

type fakeProvider struct {
	request              Request
	raw                  []byte
	base                 string
	head                 string
	file                 File
	reviews              []Review
	writes               int
	commits              int
	opens                int
	verifyErr            error
	lostCommit, lostOpen bool
	repositoryID         int64
	onFile               func()
	afterVerify          func()
}

func fakeFor(r Request, raw []byte) *fakeProvider {
	return &fakeProvider{request: r, raw: raw, base: r.Plan.Target.CommitSHA, repositoryID: 42}
}
func (f *fakeProvider) Repository(context.Context) (Repository, error) {
	return Repository{ID: f.repositoryID, Path: f.request.Source.Spec.Repository}, nil
}
func (f *fakeProvider) Head(_ context.Context, branch string) (string, error) {
	if branch == "main" {
		return f.base, nil
	}
	if f.head == "" {
		return "", ErrNotFound
	}
	return f.head, nil
}
func (f *fakeProvider) File(context.Context, string, string) (File, error) {
	if f.onFile != nil {
		f.onFile()
	}
	return File{Content: f.raw, Mode: "100644"}, nil
}
func (f *fakeProvider) Reviews(context.Context, string, string) ([]Review, error) {
	return f.reviews, nil
}
func (f *fakeProvider) Verify(_ context.Context, head, base, manifest string, file File) error {
	if f.verifyErr != nil {
		return f.verifyErr
	}
	if head != f.head || base != f.request.Plan.Target.CommitSHA || manifest != f.request.Plan.Target.ManifestPath || file.Mode != f.file.Mode || !bytes.Equal(file.Content, f.file.Content) {
		return fmt.Errorf("changed proposal")
	}
	if f.afterVerify != nil {
		f.afterVerify()
	}
	return nil
}
func (f *fakeProvider) Commit(_ context.Context, base, branch, manifest string, file File, title string) (string, error) {
	f.writes++
	f.commits++
	if f.head != "" || base != f.base {
		return "", errors.New("branch exists or base changed")
	}
	f.head, f.file = strings.Repeat("c", 40), file
	if f.lostCommit {
		f.lostCommit = false
		return "", errors.New("lost commit response")
	}
	return f.head, nil
}
func (f *fakeProvider) Open(_ context.Context, branch, base, title, body string) (Review, error) {
	f.writes++
	f.opens++
	if len(f.reviews) != 0 {
		return Review{}, errors.New("duplicate proposal")
	}
	host, suffix := "github.com", "/pull/7"
	if f.request.Plan.Origin.Provider == "gitlab" {
		host, suffix = "gitlab.com", "/-/merge_requests/7"
	}
	r := Review{Number: 7, URL: "https://" + host + "/shop/platform" + suffix, State: "open", Branch: branch, Base: base, Head: f.head, Body: body, RepositoryID: 42, TargetRepositoryID: 42}
	f.reviews = []Review{r}
	if f.lostOpen {
		f.lostOpen = false
		return Review{}, errors.New("lost review response")
	}
	return r, nil
}

func TestPromotionProposalPreservesYAMLAndHasNoPreviewWrites(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			r, raw := fixture(t, provider)
			f := fakeFor(r, raw)
			out, err := Run(t.Context(), r, f, false, nil)
			if err != nil || out.Status != "preview" || f.writes != 0 || !out.RequiresTargetQualification {
				t.Fatal(out, err, f.writes)
			}
			content, err := exportManifest(r.Plan, raw)
			if err != nil {
				t.Fatal(err)
			}
			for _, preserved := range []string{"# production configuration stays here", "MODE: production-private # keep comment", "TOKEN: secret:PRODUCTION_TOKEN"} {
				if !bytes.Contains(content, []byte(preserved)) {
					t.Fatalf("lost configuration/comment: %s", preserved)
				}
			}
			_, body := describe(r.Plan)
			encoded, _ := json.Marshal(out)
			if strings.Contains(body, "production-private") || strings.Contains(body, "PRODUCTION_TOKEN") || bytes.Contains(encoded, []byte("PRODUCTION_TOKEN")) || !strings.Contains(body, r.Plan.PlanHash) || !strings.Contains(body, "fresh target qualification") {
				t.Fatal("redaction or review contract missing")
			}
		})
	}
}

func TestPromotionProposalCreatesOnceAndRecoversUnknownWrites(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		for _, lost := range []string{"none", "commit", "review"} {
			t.Run(provider+"/"+lost, func(t *testing.T) {
				r, raw := fixture(t, provider)
				f := fakeFor(r, raw)
				f.lostCommit, f.lostOpen = lost == "commit", lost == "review"
				checks := 0
				recheck := func(context.Context) error { checks++; return nil }
				out, err := Run(t.Context(), r, f, true, recheck)
				if lost == "none" && (err != nil || out.Status != "created" || checks != 2) {
					t.Fatal(out, err, checks)
				}
				if lost != "none" && err == nil {
					t.Fatal("uncertain write silently retried")
				}
				out, err = Run(t.Context(), r, f, true, recheck)
				want := "existing"
				if lost == "commit" {
					want = "created"
				}
				if err != nil || out.Status != want || f.commits != 1 || f.opens != 1 || out.Head != f.head {
					t.Fatal(out, err, f.commits, f.opens)
				}
				out, err = Run(t.Context(), r, f, true, recheck)
				if err != nil || out.Status != "existing" || f.writes != 2 {
					t.Fatal(out, err, f.writes)
				}
			})
		}
	}
}

func TestPromotionProposalRejectsStaleOrConflictingInputsBeforeWrite(t *testing.T) {
	for _, mode := range []string{"hash", "generation", "intent", "repository", "tag", "detached", "suspended", "blocked", "unchanged", "base-moved", "wrong-provider-repo", "invalid-file", "unsafe-repository"} {
		t.Run(mode, func(t *testing.T) {
			r, raw := fixture(t, "github")
			switch mode {
			case "hash":
				r.ExpectedPlanHash = strings.Repeat("f", 64)
			case "generation":
				r.Source.Generation++
			case "intent":
				r.Source.IntentVersion++
			case "repository":
				r.Source.Spec.RepositoryID++
			case "tag":
				r.Source.Spec.Ref = "refs/tags/main"
			case "detached":
				r.Source.Detached = true
			case "suspended":
				r.Source.Suspended = true
			case "blocked":
				r.Plan.BlockingReasons = []string{"blocked"}
			case "unchanged":
				r.Plan.Changes = nil
			case "invalid-file":
				raw = []byte("PRIVATE_INVALID_MANIFEST")
			case "unsafe-repository":
				r.Source.Spec.Repository = "shop/{repo}"
			}
			if mode == "blocked" || mode == "unchanged" {
				var err error
				r.Plan, err = promotion.Finish(r.Plan, r.Plan.TargetPlan)
				if err != nil {
					t.Fatal(err)
				}
				r.ExpectedPlanHash = r.Plan.PlanHash
			}
			f := fakeFor(r, raw)
			if mode == "base-moved" {
				f.base = strings.Repeat("f", 40)
			}
			if mode == "wrong-provider-repo" {
				f.repositoryID++
			}
			_, err := Run(t.Context(), r, f, true, func(context.Context) error { return nil })
			if err == nil || f.writes != 0 || strings.Contains(err.Error(), "PRIVATE_INVALID_MANIFEST") {
				t.Fatal(err, f.writes)
			}
		})
	}
}

func TestPromotionProposalRejectsChangedOrClosedReviewsAndBranches(t *testing.T) {
	for _, mode := range []string{"closed", "body", "foreign-fork", "target-repo", "url", "head", "branch", "base", "missing-head", "duplicate", "extra-file"} {
		t.Run(mode, func(t *testing.T) {
			r, raw := fixture(t, "github")
			f := fakeFor(r, raw)
			if _, err := Run(t.Context(), r, f, true, func(context.Context) error { return nil }); err != nil {
				t.Fatal(err)
			}
			p := &f.reviews[0]
			switch mode {
			case "closed":
				p.State = "closed"
			case "body":
				p.Body += "changed"
			case "foreign-fork":
				p.RepositoryID++
			case "target-repo":
				p.TargetRepositoryID++
			case "url":
				p.URL = "https://attacker.example/proposal"
			case "head":
				p.Head = strings.Repeat("f", 40)
			case "branch":
				p.Branch = "other"
			case "base":
				p.Base = "other"
			case "missing-head":
				f.head = ""
			case "duplicate":
				f.reviews = append(f.reviews, *p)
			case "extra-file":
				f.verifyErr = errors.New("unexpected files")
			}
			if _, err := Run(t.Context(), r, f, true, func(context.Context) error { return nil }); err == nil || f.writes != 2 {
				t.Fatal(err, f.writes)
			}
		})
	}
}

func TestPromotionProposalRechecksImmediatelyBeforeWrites(t *testing.T) {
	for _, mode := range []string{"no-recheck", "changed-before-commit", "changed-before-review", "base-before-commit", "base-before-review", "head-before-review"} {
		t.Run(mode, func(t *testing.T) {
			r, raw := fixture(t, "gitlab")
			f := fakeFor(r, raw)
			checks := 0
			var recheck func(context.Context) error
			if mode != "no-recheck" {
				recheck = func(context.Context) error {
					checks++
					if mode == "changed-before-commit" || mode == "changed-before-review" && checks == 2 {
						return errors.New("fresh plan changed")
					}
					if mode == "base-before-commit" || mode == "base-before-review" && checks == 2 {
						f.base = strings.Repeat("f", 40)
					}
					if mode == "head-before-review" && checks == 2 {
						f.head = strings.Repeat("f", 40)
					}
					return nil
				}
			}
			if _, err := Run(t.Context(), r, f, true, recheck); err == nil || f.opens != 0 {
				t.Fatal(err, f.opens)
			}
			if (mode == "no-recheck" || strings.HasSuffix(mode, "before-commit")) && f.commits != 0 {
				t.Fatal("wrote after stale observation")
			}
		})
	}
}

func TestPromotionProposalObservationIgnoresTimeButBindsTarget(t *testing.T) {
	r, _ := fixture(t, "github")
	fresh := r
	fresh.Plan.ObservedAt = time.Now()
	if err := ValidateObservation(r, fresh); err != nil {
		t.Fatal(err)
	}
	fresh.Source.Spec.Ref = "refs/heads/other"
	if err := ValidateObservation(r, fresh); err == nil {
		t.Fatal("binding change accepted")
	}
}

func TestPromotionProposalRejectsHeadsMovingDuringPreviewOrRetry(t *testing.T) {
	r, raw := fixture(t, "github")
	f := fakeFor(r, raw)
	f.onFile = func() { f.base = strings.Repeat("f", 40) }
	if _, err := Run(t.Context(), r, f, false, nil); err == nil || f.writes != 0 {
		t.Fatal("stale preview", err, f.writes)
	}
	f = fakeFor(r, raw)
	if _, err := Run(t.Context(), r, f, true, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	f.afterVerify = func() { f.head = strings.Repeat("f", 40) }
	if _, err := Run(t.Context(), r, f, true, func(context.Context) error { return nil }); err == nil || f.writes != 2 {
		t.Fatal("changed retry head", err, f.writes)
	}
}

func TestPromotionProposalExportsRetainedSignedBuildWithoutTargetConfiguration(t *testing.T) {
	r, raw := fixture(t, "github")
	definition, err := gregalemanifest.ParseEnvironment(raw)
	if err != nil {
		t.Fatal(err)
	}
	before := api.EnvironmentWorkloadSource{Kind: "source", Directory: "apps/api"}
	workload := definition.Workloads["api"]
	workload.Source = &before
	definition.Workloads["api"] = workload
	base, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(base.Definition)
	if err != nil {
		t.Fatal(err)
	}
	after := before
	after.BuildArtifactID = "018f22b4-1d51-7d55-a5d8-fdc06ec7c663"
	after.BuildArtifactHash = strings.Repeat("f", 64)
	workload.Source = &after
	definition.Workloads["api"] = workload
	proposed, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	r.Plan.Target.DefinitionDigest, r.Plan.Target.ProposedDigest = base.Digest, proposed.Digest
	r.Plan.Changes = []api.EnvironmentGitPromotionChange{{Resource: "workload/api", Before: before, After: after}}
	r.Plan.Artifacts = []api.EnvironmentGitPromotionArtifact{{Resource: "workload/api", DeploymentID: "deployment", BuildID: after.BuildArtifactID, Kind: "github", BuildArtifactID: after.BuildArtifactID, BuildArtifactHash: after.BuildArtifactHash, ArtifactDigest: "sha256:" + strings.Repeat("e", 64), BuildCommitSHA: strings.Repeat("b", 40)}}
	r.Plan.TargetPlan.DesiredDigest = proposed.Digest
	r.Plan, err = promotion.Finish(r.Plan, r.Plan.TargetPlan)
	if err != nil {
		t.Fatal(err)
	}
	r.ExpectedPlanHash = r.Plan.PlanHash
	f := fakeFor(r, raw)
	if _, err := Run(t.Context(), r, f, true, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	_, body := describe(r.Plan)
	for _, identity := range []string{after.BuildArtifactID, after.BuildArtifactHash, r.Plan.Artifacts[0].ArtifactDigest, r.Plan.Artifacts[0].BuildCommitSHA} {
		if !strings.Contains(body, identity) {
			t.Fatal("missing artifact provenance", identity)
		}
	}
	if strings.Contains(body, "PRODUCTION_TOKEN") || !bytes.Contains(f.file.Content, []byte("PRODUCTION_TOKEN")) {
		t.Fatal("configuration leaked or lost")
	}
}
