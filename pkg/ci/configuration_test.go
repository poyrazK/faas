package ci

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCIConfigurationValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Job)
	}{
		{"invalid-name", func(j *Job) { j.Env = map[string]string{"A-B": "value"} }},
		{"empty-name", func(j *Job) { j.Env = map[string]string{"": "value"} }},
		{"name-length", func(j *Job) { j.Env = map[string]string{strings.Repeat("A", api.CIEnvironmentNameMaxBytes+1): ""} }},
		{"value-length", func(j *Job) {
			j.Env = map[string]string{"VALUE": strings.Repeat("v", api.CIEnvironmentValueMaxBytes+1)}
		}},
		{"nul", func(j *Job) { j.Env = map[string]string{"VALUE": "a\x00b"} }},
		{"utf8", func(j *Job) { j.Steps[0].Env = map[string]string{"VALUE": string([]byte{255})} }},
		{"count", func(j *Job) { j.Env = configurationEnvironment(api.CIMaxEnvironmentVariables+1, "") }},
		{"merged-count", func(j *Job) {
			j.Env = configurationEnvironment(api.CIMaxEnvironmentVariables, "")
			j.Steps[0].Env = map[string]string{"OTHER": ""}
		}},
		{"merged-bytes", func(j *Job) {
			j.Env = configurationEnvironment(4, strings.Repeat("x", 1024))
			j.Steps[0].Env = map[string]string{"A": strings.Repeat("a", 1024), "B": strings.Repeat("b", 1024), "C": strings.Repeat("c", 1024), "D": strings.Repeat("d", 1024)}
		}},
		{"total-bytes", func(j *Job) { j.Env = configurationEnvironment(8, strings.Repeat("v", 1024)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def := executionDefinition()
			job := def.Jobs["test"]
			tc.change(&job)
			def.Jobs["test"] = job
			if _, _, err := Compile(def); err == nil {
				t.Fatal("invalid environment accepted")
			}
		})
	}
	for _, name := range []string{"PATH", "HOME", "LANG", "CI", "PWD", "OLDPWD", "SHELL", "ENV", "BASH_ENV", "IFS", "CDPATH", "BASHOPTS", "SHELLOPTS", "FAAS_JOB", "GREGALE_CI_CAPABILITY", "LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "gregale_ci_workspace"} {
		for _, step := range []bool{false, true} {
			job := executionDefinition().Jobs["test"]
			if step {
				job.Steps[0].Env = map[string]string{name: "override"}
			} else {
				job.Env = map[string]string{name: "override"}
			}
			if validateJobConfiguration(job) == nil {
				t.Errorf("reserved environment accepted: %s, step=%v", name, step)
			}
		}
	}
	for _, dir := range []string{"/tmp", "../outside", "a/../b", "a/./b", "a//b", "a/", "a\\b", "a\x00b", "a\nb", "a\tb", strings.Repeat("a", api.CIWorkingDirectoryMaxBytes+1), string([]byte{255})} {
		job := executionDefinition().Jobs["test"]
		job.WorkingDirectory = dir
		if validateJobConfiguration(job) == nil {
			t.Errorf("invalid job directory accepted: %q", dir)
		}
		job.WorkingDirectory, job.Steps[0].WorkingDirectory = "", dir
		if validateJobConfiguration(job) == nil {
			t.Errorf("invalid step directory accepted: %q", dir)
		}
	}
	job := executionDefinition().Jobs["test"]
	job.Env = configurationEnvironment(api.CIMaxEnvironmentVariables, "")
	job.Steps[0].Env = map[string]string{"VAR_0": "empty override\n$literal=${nothing}"}
	job.WorkingDirectory, job.Steps[0].WorkingDirectory = "packages/my app", "."
	if err := validateJobConfiguration(job); err != nil {
		t.Fatal(err)
	}
}

func configurationEnvironment(count int, value string) map[string]string {
	env := make(map[string]string, count)
	for i := range count {
		env[fmt.Sprintf("VAR_%d", i)] = value
	}
	return env
}

func TestCIConfigurationCanonicalSnapshotsAndStrictParsing(t *testing.T) {
	def := executionDefinition()
	job := def.Jobs["test"]
	job.Env, job.WorkingDirectory = map[string]string{"Z": "", "A": "literal $Z\nsecond line"}, "packages/api"
	job.Steps[0].Env, job.Steps[1].WorkingDirectory = map[string]string{"A": "step"}, "."
	def.Jobs["test"] = job
	before, _ := json.Marshal(def)
	plan, raw, err := Compile(def)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(def)
	if !bytes.Equal(before, after) {
		t.Fatal("configuration normalization mutated caller")
	}
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, againRaw, err := Compile(parsed)
	if err != nil || !bytes.Equal(raw, againRaw) || !reflect.DeepEqual(plan, again) {
		t.Fatal("configuration digest is not stable")
	}
	job.Env["A"], job.Steps[0].Env["A"] = "mutated job", "mutated step"
	if plan.Jobs[0].Env["A"] != "literal $Z\nsecond line" || plan.Jobs[0].Steps[0].Env["A"] != "step" {
		t.Fatal("plan aliases caller environment maps")
	}
	for _, value := range []string{"true", "42", "null", "[]", "{}"} {
		for _, scope := range []string{"job", "step"} {
			yaml := "version: 1\nrepository: gregale/example\njobs:\n  test:\n    image: registry.example/ci@sha256:" + strings.Repeat("a", 64) + "\n"
			if scope == "job" {
				yaml += "    env: {VALUE: " + value + "}\n    steps: [{name: test, run: echo}]\n"
			} else {
				yaml += "    steps:\n      - name: test\n        run: echo\n        env: {VALUE: " + value + "}\n"
			}
			if _, err := Parse([]byte(yaml)); err == nil {
				t.Errorf("coerced non-string %s environment %s", scope, value)
			}
			if _, err := Parse([]byte(strings.ReplaceAll(yaml, "env: {VALUE: "+value+"}", "working_directory: "+value))); err == nil {
				t.Errorf("coerced non-string %s working directory %s", scope, value)
			}
		}
	}
	_, legacy, err := Compile(executionDefinition())
	if err != nil || bytes.Contains(legacy, []byte(`"env"`)) || bytes.Contains(legacy, []byte(`"working_directory"`)) {
		t.Fatal("legacy canonical definition changed")
	}
}

func TestCIConfigurationManifestVersions(t *testing.T) {
	key := AttemptKey{AccountID: uuid.NewString(), ExecutionID: uuid.NewString(), AttemptID: uuid.NewString(), JobName: "test", Fence: 1}
	digest := "sha256:" + strings.Repeat("a", 64)
	source := SourceArchive{Key: jobsObjectPrefix(key) + "source/" + strings.Repeat("a", 64) + ".tar.gz", Digest: digest, Bytes: 1}
	for _, configured := range []string{"legacy", "job-env", "step-env", "job-dir", "step-dir"} {
		for _, inputs := range []bool{false, true} {
			job := executionDefinition().Jobs["test"]
			switch configured {
			case "job-env":
				job.Env = map[string]string{"BUILD_MODE": "test"}
			case "step-env":
				job.Steps[0].Env = map[string]string{"BUILD_MODE": "test"}
			case "job-dir":
				job.WorkingDirectory = "packages/api"
			case "step-dir":
				job.Steps[0].WorkingDirectory = "."
			}
			var resolved []JobsInput
			want := 1
			if inputs {
				job.Needs = []string{"build"}
				input := ArtifactInput{Name: "app", Job: "build", Path: "dist/app"}
				job.Inputs = []ArtifactInput{input}
				resolved = []JobsInput{{ArtifactInput: input, AttemptID: uuid.NewString(), Digest: digest, Bytes: 1}}
				want = 2
			}
			if configured != "legacy" {
				want = 3
			}
			raw, err := jobsManifest(StartRequest{Key: key, Repository: "gregale/example", CommitSHA: strings.Repeat("a", 40), Deadline: time.Now().Add(time.Minute), Job: PlannedJob{Name: "test", Job: job}}, source, resolved...)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := DecodeJobsManifest(base64.StdEncoding.EncodeToString(raw))
			if err != nil || manifest.Version != want {
				t.Fatalf("%s inputs=%v: version=%d err=%v", configured, inputs, manifest.Version, err)
			}
			for _, version := range []int{0, 1, 2, 3, 4} {
				bad := clone(manifest)
				bad.Version = version
				if (ValidateJobsManifest(bad) == nil) != (version == want) {
					t.Fatalf("configuration accepted wrong manifest version %d, want %d", version, want)
				}
			}
		}
	}
}

func TestCIConfigurationSurvivesJobsRestart(t *testing.T) {
	h := newJobsHarness(t)
	def := executionDefinition()
	job := def.Jobs["test"]
	job.Env, job.WorkingDirectory = map[string]string{"BUILD_MODE": "job"}, "packages/api"
	job.Steps[0].Env, job.Steps[1].WorkingDirectory = map[string]string{"BUILD_MODE": "step"}, "."
	def.Jobs["test"] = job
	revision, err := h.store.PutRevision(t.Context(), h.account, "configuration", def)
	if err != nil {
		t.Fatal(err)
	}
	binding := h.binding.GitHubBindingRequest
	binding.RevisionID = revision.ID
	if _, err := h.store.PutGitHubBinding(t.Context(), h.account, h.binding.Name, binding); err != nil {
		t.Fatal(err)
	}
	h.req.RevisionID = revision.ID
	row := h.tick(t, h.admit(t, "configuration"))
	proof, view := inputProof(t, h, row, "test")
	if view.Manifest.Version != 3 || !reflect.DeepEqual(view.Manifest.Job.Env, job.Env) || view.Manifest.Job.WorkingDirectory != job.WorkingDirectory {
		t.Fatal("native command lost configuration")
	}
	before, _ := json.Marshal(view.Manifest)
	h.restart()
	again, err := h.store.AuthorizeJobsRuntime(t.Context(), h.backend.Config, proof, RuntimeControl)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(again.Manifest)
	if !bytes.Equal(before, after) {
		t.Fatal("restart changed frozen step configuration")
	}
	var nativeEnv []byte
	if err := h.pool.QueryRow(t.Context(), "SELECT effective_env_snapshot FROM job_runs WHERE id=$1", proof.RunID).Scan(&nativeEnv); err != nil {
		t.Fatal(err)
	}
	if string(nativeEnv) != "{}" {
		t.Fatal("CI variables escaped into the native runner environment")
	}
}
