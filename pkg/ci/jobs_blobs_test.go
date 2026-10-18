package ci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func jobsBlobFixture(t *testing.T) (JobsSourceRequest, PlannedJob, *jobsTestBlobs, JobsResultManifest) {
	t.Helper()
	key := AttemptKey{AccountID: uuid.NewString(), ExecutionID: uuid.NewString(), AttemptID: uuid.NewString(), JobName: "test", Fence: 1}
	req := JobsSourceRequest{Key: key, CommitSHA: strings.Repeat("a", 40), Repository: "gregale/ci-qualification",
		Binding: GitHubBinding{ID: uuid.NewString(), AccountID: key.AccountID, Enabled: true, Generation: 1, Repository: "gregale/ci-qualification", GitHubBindingRequest: GitHubBindingRequest{InstallationID: 12, RepositoryID: 34}}}
	plan, _, err := Compile(executionDefinition())
	if err != nil {
		t.Fatal(err)
	}
	job := plan.Jobs[0]
	logs, artifact := []byte("synthetic logs\x00\xff"), []byte("report bytes")
	prefix := jobsObjectPrefix(key) + "output/"
	zero := 0
	result := JobsResultManifest{Version: 1, AccountID: key.AccountID, ExecutionID: key.ExecutionID, AttemptID: key.AttemptID, CommitSHA: req.CommitSHA, Status: StatusSucceeded, ExitCode: &zero,
		Logs: SourceArchive{Key: prefix + "logs.bin", Digest: jobsDigest(logs), Bytes: int64(len(logs))}, Artifacts: []JobsArtifact{{ArtifactRef: ArtifactRef{Path: "reports/unit.xml", Bytes: int64(len(artifact)), Digest: jobsDigest(artifact)}, Key: prefix + "artifacts/reports/unit.xml"}}}
	for _, step := range job.Steps {
		result.Steps = append(result.Steps, StepResult{Name: step.Name, Status: StatusSucceeded, ExitCode: &zero})
	}
	blobs := &jobsTestBlobs{data: map[string][]byte{result.Logs.Key: logs, result.Artifacts[0].Key: artifact}}
	return req, job, blobs, result
}

func TestCIJobsBlobOutput(t *testing.T) {
	cases := []struct {
		name   string
		change func(*JobsResultManifest, *jobsTestBlobs)
		raw    func([]byte) []byte
		valid  bool
	}{
		{name: "valid", valid: true},
		{name: "foreign_account", change: func(r *JobsResultManifest, _ *jobsTestBlobs) { r.AccountID = uuid.NewString() }},
		{name: "foreign_attempt", change: func(r *JobsResultManifest, _ *jobsTestBlobs) { r.AttemptID = uuid.NewString() }},
		{name: "different_commit", change: func(r *JobsResultManifest, _ *jobsTestBlobs) { r.CommitSHA = strings.Repeat("b", 40) }},
		{name: "foreign_artifact", change: func(r *JobsResultManifest, _ *jobsTestBlobs) {
			r.Artifacts[0].Key = "ci/accounts/another/output/artifacts/reports/unit.xml"
		}},
		{name: "traversal", change: func(r *JobsResultManifest, _ *jobsTestBlobs) { r.Artifacts[0].Path = "reports/../../other" }},
		{name: "undeclared_artifact", change: func(r *JobsResultManifest, _ *jobsTestBlobs) { r.Artifacts[0].Path = "secret.txt" }},
		{name: "changed_artifact_bytes", change: func(r *JobsResultManifest, b *jobsTestBlobs) { b.data[r.Artifacts[0].Key] = []byte("forged bytes") }},
		{name: "larger_artifact", change: func(r *JobsResultManifest, b *jobsTestBlobs) {
			b.data[r.Artifacts[0].Key] = append(b.data[r.Artifacts[0].Key], 1)
		}},
		{name: "missing_artifact", change: func(r *JobsResultManifest, b *jobsTestBlobs) { delete(b.data, r.Artifacts[0].Key) }},
		{name: "artifact_cap", change: func(r *JobsResultManifest, _ *jobsTestBlobs) { r.Artifacts[0].Bytes = api.CIArtifactMaxBytes + 1 }},
		{name: "duplicate_artifact", change: func(r *JobsResultManifest, _ *jobsTestBlobs) { r.Artifacts = append(r.Artifacts, r.Artifacts[0]) }},
		{name: "foreign_logs", change: func(r *JobsResultManifest, _ *jobsTestBlobs) { r.Logs.Key = "other/logs.bin" }},
		{name: "changed_logs", change: func(r *JobsResultManifest, b *jobsTestBlobs) { b.data[r.Logs.Key] = []byte("changed logs") }},
		{name: "log_cap", change: func(r *JobsResultManifest, _ *jobsTestBlobs) { r.Logs.Bytes = api.CILogMaxBytesPerAttempt + 1 }},
		{name: "unsupported_version", change: func(r *JobsResultManifest, _ *jobsTestBlobs) { r.Version = 2 }},
		{name: "unfinished_steps", change: func(r *JobsResultManifest, _ *jobsTestBlobs) { r.Steps[0].Status = StatusRunning }},
		{name: "scheduler_disagrees", change: func(r *JobsResultManifest, _ *jobsTestBlobs) { one := 1; r.Status = StatusFailed; r.ExitCode = &one }},
		{name: "duplicate_json", raw: func(raw []byte) []byte { return append([]byte(`{"version":1,`), raw[1:]...) }},
		{name: "case_duplicate_json", raw: func(raw []byte) []byte { return append([]byte(`{"VERSION":1,`), raw[1:]...) }},
		{name: "trailing_json", raw: func(raw []byte) []byte { return append(raw, []byte(` {}`)...) }},
		{name: "unknown_field", raw: func(raw []byte) []byte { return append([]byte(`{"access_token":"never accepted",`), raw[1:]...) }},
		{name: "oversized_manifest", raw: func(_ []byte) []byte { return bytes.Repeat([]byte(" "), api.CIJobsResultManifestMaxBytes+1) }},
		{name: "nested_json", raw: func(_ []byte) []byte {
			return []byte(strings.Repeat("[", api.CIJobsResultJSONMaxDepth+1) + "0" + strings.Repeat("]", api.CIJobsResultJSONMaxDepth+1))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, job, blobs, result := jobsBlobFixture(t)
			if tc.change != nil {
				tc.change(&result, blobs)
			}
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if tc.raw != nil {
				raw = tc.raw(raw)
			}
			blobs.data[jobsObjectPrefix(req.Key)+"output/result.json"] = raw
			zero := 0
			out, err := (BlobJobsOutput{Blobs: blobs}).Collect(t.Context(), req, job, "succeeded", &zero)
			if tc.valid {
				if err != nil || len(out.Artifacts) != 1 || len(out.Logs) == 0 {
					t.Fatalf("valid output refused: %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid output accepted")
			}
		})
	}
}

func TestCIJobsSourceArchive(t *testing.T) {
	for _, name := range []string{"valid", "different_sha", "foreign_binding", "access_revoked", "empty", "over_limit", "context_cancelled"} {
		t.Run(name, func(t *testing.T) {
			req, _, blobs, _ := jobsBlobFixture(t)
			stream := &jobsTestStream{body: []byte("opaque source archive")}
			verify := func(context.Context, string, int64, int64, string) error { return nil }
			ctx := t.Context()
			switch name {
			case "different_sha":
				stream.sha = strings.Repeat("b", 40)
			case "foreign_binding":
				req.Binding.AccountID = uuid.NewString()
			case "access_revoked":
				verify = func(context.Context, string, int64, int64, string) error { return errors.New("credential-supersecret") }
			case "empty":
				stream.body = nil
			case "over_limit":
				stream.body = bytes.Repeat([]byte("a"), api.CIJobsSourceArchiveMaxBytes+1)
			case "context_cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			archive, err := (GitHubArchiveSource{Verify: verify, Stream: stream, Blobs: blobs}).Stage(ctx, req)
			if name != "valid" {
				if err == nil {
					t.Fatal("invalid source accepted")
				}
				if strings.Contains(err.Error(), "supersecret") {
					t.Fatal("source credential leaked")
				}
				return
			}
			if err != nil || validateSourceArchive(req.Key, archive) != nil || archive.Digest != jobsDigest(stream.body) || !bytes.Equal(blobs.data[archive.Key], stream.body) {
				t.Fatalf("source was not pinned, hashed and scoped: %v", err)
			}
			if stream.account != req.Key.AccountID || stream.repository != req.Repository || stream.installation != req.Binding.InstallationID {
				t.Fatal("source authority was not forwarded exactly")
			}
		})
	}
}

func TestCIJobsDispatchConfig(t *testing.T) {
	account := uuid.NewString()
	for _, value := range []string{"", "0", "true", "yes", "2", "1"} {
		cfg := ConfigFromEnv(func(key string) string {
			switch key {
			case InternalEnv, GitHubEnv:
				return "1"
			case AccountsEnv:
				return account
			case DispatchEnv:
				return value
			}
			return ""
		})
		if cfg.DispatchAllows(account) != (value == "1") {
			t.Fatalf("dispatch flag %q was not exact-1", value)
		}
		if cfg.DispatchAllows(uuid.NewString()) {
			t.Fatal("unlisted account could dispatch")
		}
	}
	cfg := ConfigFromEnv(func(key string) string {
		if key == AccountsEnv {
			return account + ",bad"
		}
		return "1"
	})
	if cfg.DispatchAllows(account) {
		t.Fatal("malformed allowlist enabled compute")
	}
}
