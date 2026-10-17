package ci

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCIJUnitDefinitionAndGuestCompatibility(t *testing.T) {
	def := executionDefinition()
	legacy, canonical, err := Compile(def)
	if err != nil {
		t.Fatal(err)
	}
	// The pre-report canonical v1 shape has no new member or digest change.
	if bytes.Contains(canonical, []byte(`"junit"`)) || legacy.DefinitionDigest != jobsDigest(canonical) {
		t.Fatal("legacy canonical definition changed")
	}
	for _, paths := range [][]string{{"reports/*.xml"}, {"reports/../unit.xml"}, {"/unit.xml"}, {"other/unit.xml"}, {"reports/unit.xml", "reports/unit.xml"}, {"reports/a.xml", "reports/b.xml", "reports/c.xml"}} {
		candidate := clone(def)
		job := candidate.Jobs["test"]
		job.JUnit = paths
		candidate.Jobs["test"] = job
		if _, _, err := Compile(candidate); err == nil {
			t.Fatalf("invalid report selector accepted: %v", paths)
		}
	}
	job := def.Jobs["test"]
	job.JUnit = []string{"reports/z.xml", "reports/a.xml"}
	def.Jobs["test"] = job
	before := clone(def)
	plan, _, err := Compile(def)
	if err != nil || !reflect.DeepEqual(before, def) || plan.Jobs[0].JUnit[0] != "reports/a.xml" || plan.DefinitionDigest == legacy.DefinitionDigest {
		t.Fatalf("report selection was not immutable/canonical: %v", err)
	}
	raw, err := jobsManifest(StartRequest{Job: plan.Jobs[0]}, SourceArchive{})
	if err != nil || bytes.Contains(raw, []byte(`"junit"`)) || len(plan.Jobs[0].JUnit) != 2 {
		t.Fatal("controller selectors changed the guest protocol or plan")
	}
}

type junitCountingBlobs struct {
	store JobsBlobStore
	gets  map[string]int
}

func (b *junitCountingBlobs) Put(ctx context.Context, key string, reader io.Reader) error {
	return b.store.Put(ctx, key, reader)
}
func (b *junitCountingBlobs) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	b.gets[key]++
	return b.store.Get(ctx, key)
}

func TestCIJUnitVerifiedCollectionAndNativeAuthority(t *testing.T) {
	for _, name := range []string{"valid", "native_failure", "missing", "invalid", "oversized", "checksum", "foreign_key", "guest_report_field"} {
		t.Run(name, func(t *testing.T) {
			req, job, blobs, result := jobsBlobFixture(t)
			job.JUnit = []string{"reports/unit.xml"}
			raw := []byte(junitExample)
			want, native, code := ReportParsed, StatusSucceeded, 0
			switch name {
			case "native_failure":
				native, code = StatusFailed, 137
			case "invalid":
				raw, want = []byte("<testsuite><testcase>"), ReportInvalid
			case "oversized":
				raw, want = bytes.Repeat([]byte{'x'}, api.CIJUnitMaxBytes+1), ReportLimit
			case "missing":
				want = ReportMissing
			}
			ref := &result.Artifacts[0]
			ref.Digest, ref.Bytes = jobsDigest(raw), int64(len(raw))
			blobs.data[ref.Key] = raw
			artifactKey := ref.Key
			if name == "checksum" {
				blobs.data[ref.Key] = []byte("unverified")
			}
			if name == "foreign_key" {
				ref.Key = "foreign/artifacts/reports/unit.xml"
			}
			if name == "missing" {
				result.Artifacts = nil
			}
			sealed, _ := json.Marshal(result)
			if name == "guest_report_field" {
				sealed = append([]byte(`{"test_reports":[],`), sealed[1:]...)
			}
			counting := &junitCountingBlobs{store: blobs, gets: map[string]int{}}
			out, err := (SealedJobsOutput{Store: runtimeResultBytes(sealed), Blobs: counting}).Collect(t.Context(), req, job, native, &code)
			if name == "checksum" || name == "foreign_key" || name == "guest_report_field" {
				if err == nil {
					t.Fatal("unverified/guest-provided report accepted")
				}
				return
			}
			if err != nil || out.Status != native || out.ExitCode == nil || *out.ExitCode != code || len(out.TestReports) != 1 || out.TestReports[0].Status != want {
				t.Fatalf("report diagnostics changed native result: %+v %v", out, err)
			}
			if err := validateTestReports(job, out); err != nil {
				t.Fatal(err)
			}
			if name != "missing" && counting.gets[artifactKey] != 1 {
				t.Fatal("report collection performed an extra provider read")
			}
			if want == ReportParsed && out.TestReports[0].Failed != 1 {
				t.Fatal("reported failure disappeared")
			}
		})
	}
}

func TestCIJUnitProjectionRejectsIdentityAndDrainMutation(t *testing.T) {
	_, job, _, manifest := jobsBlobFixture(t)
	job.JUnit = []string{"reports/unit.xml"}
	report := junitReport(t, junitExample)
	zero := 0
	observation := Observation{Status: StatusSucceeded, ExitCode: &zero, Steps: manifest.Steps, Artifacts: []ArtifactRef{report.ArtifactRef}, TestReports: []TestReport{report}}
	previous := Attempt{ID: uuid.NewString(), BackendStatus: StatusSucceeded, ExitCode: &zero, Steps: manifest.Steps, Artifacts: observation.Artifacts, TestReports: observation.TestReports}
	if err := validateObservation(job, previous, observation); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Observation){
		func(o *Observation) { o.TestReports[0].Digest = "sha256:" + strings.Repeat("b", 64) },
		func(o *Observation) { o.TestReports[0].Path = "reports/other.xml" },
		func(o *Observation) { o.TestReports[0].Passed++ },
		func(o *Observation) { o.TestReports[0].Failures[0].Index = 0 },
		func(o *Observation) { o.TestReports[0].Failures[0].Message = "changed while draining" },
		func(o *Observation) { o.TestReports = nil },
	} {
		candidate := clone(observation)
		change(&candidate)
		if err := validateObservation(job, previous, candidate); err == nil {
			t.Fatal("terminal report mutation accepted during log draining")
		}
	}
	missing := completeObservationReports(job, Observation{Status: StatusCancelled})
	if len(missing.TestReports) != 1 || missing.TestReports[0].Status != ReportMissing || validateTestReports(job, missing) != nil {
		t.Fatal("no-VM cancellation lost its report diagnostic")
	}
	uncollected := completeObservationReports(job, Observation{Status: StatusCancelled, Artifacts: observation.Artifacts})
	if uncollected.TestReports[0].Status != ReportUncollected || validateTestReports(job, uncollected) != nil {
		t.Fatal("uncollected artifact was interpreted as test results")
	}
}

func TestCIJUnitFitsExistingSnapshotAndArchiveBudgets(t *testing.T) {
	// Exercise the combined limits: escaped failure detail, two reports, all
	// artifact slots, retained logs and all jobs must fit the existing budgets.
	text := strings.Repeat("&lt;&amp;", api.CIJUnitDetailMaxBytes/2)
	entry := `<testcase name="` + text + `" classname="` + text + `"><failure message="` + text + `">` + text + `</failure></testcase>`
	report := junitReport(t, "<testsuite>"+strings.Repeat(entry, api.CIJUnitMaxFailures)+"</testsuite>")
	if report.Status != ReportParsed || !report.Truncated {
		t.Fatal("budget fixture did not exercise bounded escaped details")
	}
	zero := 0
	observation := Observation{Status: StatusSucceeded, ExitCode: &zero, Logs: bytes.Repeat([]byte{255}, api.CILogMaxBytesPerAttempt)}
	for i := 0; i < api.CIMaxArtifactsPerJob; i++ {
		name := fmt.Sprintf("reports/%02d-", i) + strings.Repeat("&", api.CIArtifactPathMaxBytes-15)
		ref := ArtifactRef{Path: name, Digest: report.Digest, Bytes: report.Bytes}
		observation.Artifacts = append(observation.Artifacts, ref)
		if i < api.CIJUnitMaxReportsPerJob {
			item := clone(report)
			item.ArtifactRef = ref
			observation.TestReports = append(observation.TestReports, boundTestReport(item))
		}
	}
	raw, _ := json.Marshal(observation)
	if len(raw) > api.CIJobsOutputSnapshotMaxBytes {
		t.Fatal("report diagnostics exceeded the existing per-job output budget")
	}
	execution := Execution{ID: uuid.NewString()}
	archive := ExecutionArchive{ExecutionID: execution.ID, RevisionID: uuid.NewString(), Pipeline: strings.Repeat("p", 40), CommitSHA: strings.Repeat("a", 40), DefinitionDigest: "sha256:" + strings.Repeat("a", 64), Status: StatusSucceeded, ProfileID: uuid.NewString(), CreatedAt: time.Now(), FinishedAt: time.Now(), ArchivedAt: time.Now()}
	for i := 0; i < api.CIMaxJobs; i++ {
		name := fmt.Sprintf("job-%02d-", i) + strings.Repeat("x", 33)
		attempt := &Attempt{ID: uuid.NewString(), Artifacts: observation.Artifacts, TestReports: observation.TestReports, Logs: observation.Logs}
		execution.Jobs = append(execution.Jobs, JobExecution{Name: name, Status: StatusSucceeded, Attempt: attempt})
		totals := reportTotals(api.CIJUnitMaxReportsPerJob, observation.TestReports)
		archive.Jobs = append(archive.Jobs, ArchivedJob{Name: name, Status: StatusSucceeded, AttemptID: attempt.ID, TestReports: &totals})
	}
	if _, err := executionJSON(execution); err != nil {
		t.Fatalf("report diagnostics exceeded the existing execution budget: %v", err)
	}
	raw, _ = json.Marshal(archive)
	if len(raw) > api.CIExecutionArchiveMaxBytes {
		t.Fatal("aggregate report history exceeded the existing archive budget")
	}
}
