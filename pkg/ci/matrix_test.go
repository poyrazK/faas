package ci

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func matrixDefinition() Definition {
	def := executionDefinition()
	job := def.Jobs["test"]
	job.Env = map[string]string{"MODE": "job", "BASE": "shared"}
	job.Matrix = map[string]MatrixVariant{
		"alpha": {Env: map[string]string{"MODE": "alpha", "VARIANT": "alpha"}},
		"beta":  {Image: def.Jobs["build"].Image, Env: map[string]string{"MODE": "beta", "VARIANT": "beta"}},
	}
	job.Steps[0].Env = map[string]string{"MODE": "step"}
	def.Jobs["test"] = job
	return def
}

func TestCIMatrixCompileDeterministicAndImmutable(t *testing.T) {
	def := matrixDefinition()
	consumer := def.Jobs["build"]
	consumer.Inputs = []ArtifactInput{{Name: "report", Job: "test-beta", Path: "reports/unit.xml"}}
	def.Jobs["build"] = consumer
	def.Jobs["selected"] = Job{Image: consumer.Image, Needs: []string{"test-alpha"}, Steps: consumer.Steps}
	before, _ := json.Marshal(def)
	plan, canonical, err := Compile(def)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(def)
	if !bytes.Equal(before, after) {
		t.Fatal("matrix compile mutated caller")
	}
	if len(plan.Jobs) != 4 || plan.Jobs[0].Name != "test-alpha" || plan.Jobs[1].Name != "test-beta" || plan.Jobs[2].Name != "build" || plan.Jobs[3].Name != "selected" {
		t.Fatalf("wrong expanded job order: %+v", plan.Jobs)
	}
	for _, job := range plan.Jobs {
		if job.Matrix != nil {
			t.Fatal("plan retained unresolved matrix")
		}
	}
	alpha, beta := plan.Jobs[0], plan.Jobs[1]
	if alpha.Image != def.Jobs["test"].Image || beta.Image != def.Jobs["test"].Matrix["beta"].Image || alpha.Env["MODE"] != "alpha" || beta.Env["MODE"] != "beta" || beta.Env["BASE"] != "shared" || beta.Steps[0].Env["MODE"] != "step" {
		t.Fatal("matrix override precedence was lost")
	}
	if !reflect.DeepEqual(plan.Jobs[2].Needs, []string{"test-alpha", "test-beta"}) || plan.Jobs[2].Inputs[0].Job != "test-beta" || !reflect.DeepEqual(plan.Jobs[3].Needs, []string{"test-alpha"}) {
		t.Fatal("dependencies or explicit artifact producer changed")
	}
	parsed, err := Parse(canonical)
	if err != nil {
		t.Fatal(err)
	}
	again, againRaw, err := Compile(parsed)
	if err != nil || !reflect.DeepEqual(plan, again) || !bytes.Equal(canonical, againRaw) {
		t.Fatal("matrix did not round trip deterministically")
	}
	// Variant insertion order cannot change either execution names or digest.
	reordered := clone(def)
	job := reordered.Jobs["test"]
	job.Matrix = map[string]MatrixVariant{}
	job.Matrix["beta"], job.Matrix["alpha"] = def.Jobs["test"].Matrix["beta"], def.Jobs["test"].Matrix["alpha"]
	reordered.Jobs["test"] = job
	ordered, orderedRaw, err := Compile(reordered)
	if err != nil || !reflect.DeepEqual(plan, ordered) || !bytes.Equal(canonical, orderedRaw) {
		t.Fatal("map order changed matrix identity")
	}
	def.Jobs["test"].Matrix["alpha"].Env["MODE"] = "changed"
	def.Jobs["test"].Steps[0].Env["MODE"] = "changed"
	plan.Jobs[0].Env["BASE"], plan.Jobs[0].Steps[0].Env["MODE"] = "changed child", "changed child"
	if beta.Env["BASE"] != "shared" || beta.Steps[0].Env["MODE"] != "step" || alpha.Env["MODE"] != "alpha" {
		t.Fatal("matrix jobs share mutable configuration")
	}
}

func TestCIMatrixRejectsAmbiguousOrUnboundedExpansion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Definition)
	}{
		{"empty-matrix", func(d *Definition) { j := d.Jobs["test"]; j.Matrix = map[string]MatrixVariant{}; d.Jobs["test"] = j }},
		{"invalid-variant", func(d *Definition) { d.Jobs["test"].Matrix["../outside"] = MatrixVariant{} }},
		{"name-length", func(d *Definition) { d.Jobs["test"].Matrix[strings.Repeat("a", 40)] = MatrixVariant{} }},
		{"collision", func(d *Definition) { d.Jobs["test-alpha"] = d.Jobs["build"] }},
		{"matrix-base-collision", func(d *Definition) { j := cloneJob(d.Jobs["test"]); d.Jobs["test-alpha"] = j }},
		{"cross-matrix-collision", func(d *Definition) {
			j := cloneJob(d.Jobs["test"])
			j.Matrix = map[string]MatrixVariant{"a-b": {}}
			d.Jobs["test"] = j
			j.Matrix = map[string]MatrixVariant{"b": {}}
			d.Jobs["test-a"] = j
		}},
		{"unpinned-variant", func(d *Definition) { d.Jobs["test"].Matrix["alpha"] = MatrixVariant{Image: "node:24"} }},
		{"variant-secret-key", func(d *Definition) {
			d.Jobs["test"].Matrix["alpha"] = MatrixVariant{Env: map[string]string{"GREGALE_CI_CAPABILITY": "override"}}
		}},
		{"merged-environment", func(d *Definition) {
			j := d.Jobs["test"]
			j.Env = configurationEnvironment(32, "")
			j.Matrix["alpha"] = MatrixVariant{Env: map[string]string{"EXTRA": "value"}}
			d.Jobs["test"] = j
		}},
		{"step-merged-environment", func(d *Definition) {
			j := d.Jobs["test"]
			j.Env = nil
			j.Matrix["alpha"] = MatrixVariant{Env: configurationEnvironment(32, "")}
			d.Jobs["test"] = j
		}},
		{"self-matrix", func(d *Definition) { j := d.Jobs["test"]; j.Needs = []string{"test"}; d.Jobs["test"] = j }},
		{"self-variant", func(d *Definition) { j := d.Jobs["test"]; j.Needs = []string{"test-alpha"}; d.Jobs["test"] = j }},
		{"cycle", func(d *Definition) { j := d.Jobs["test"]; j.Needs = []string{"build"}; d.Jobs["test"] = j }},
		{"overlapping-needs", func(d *Definition) {
			j := d.Jobs["build"]
			j.Needs = []string{"test", "test-alpha"}
			d.Jobs["build"] = j
		}},
		{"unknown-variant", func(d *Definition) { j := d.Jobs["build"]; j.Needs = []string{"test-gamma"}; d.Jobs["build"] = j }},
		{"ambiguous-input", func(d *Definition) {
			j := d.Jobs["build"]
			j.Inputs = []ArtifactInput{{Name: "report", Job: "test", Path: "reports/unit.xml"}}
			d.Jobs["build"] = j
		}},
		{"unneeded-input-variant", func(d *Definition) {
			j := d.Jobs["build"]
			j.Needs = []string{"test-alpha"}
			j.Inputs = []ArtifactInput{{Name: "report", Job: "test-beta", Path: "reports/unit.xml"}}
			d.Jobs["build"] = j
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def := matrixDefinition()
			tc.change(&def)
			if _, _, err := Compile(def); err == nil {
				t.Fatal("invalid matrix accepted")
			}
		})
	}
	def := matrixDefinition()
	for i := 0; i < 9; i++ {
		def.Jobs["test"].Matrix[fmt.Sprintf("variant-%d", i)] = MatrixVariant{}
	}
	if _, _, err := Compile(def); err == nil {
		t.Fatal("variant count bound was not enforced")
	}
	def = matrixDefinition()
	j := def.Jobs["test"]
	j.Matrix = make(map[string]MatrixVariant)
	for i := 0; i < 8; i++ {
		j.Matrix[fmt.Sprintf("v%d", i)] = MatrixVariant{}
	}
	def.Jobs["test"] = j
	other := cloneJob(j)
	def.Jobs["other"] = other
	_, _, err := Compile(def)
	var limit *LimitError
	if !errors.As(err, &limit) || limit.Resource != "expanded_jobs" || limit.Limit != 16 || limit.Observed != 17 {
		t.Fatalf("expanded count bound: %v", err)
	}
	delete(def.Jobs, "build")
	plan, _, err := Compile(def)
	if err != nil || len(plan.Jobs) != 16 {
		t.Fatalf("valid expanded bound rejected: %v", err)
	}
}

func TestCIMatrixStrictYAMLAndConcreteManifests(t *testing.T) {
	prefix := "version: 1\nrepository: gregale/example\njobs:\n  test:\n    image: registry.example/ci@sha256:" + strings.Repeat("a", 64) + "\n    steps: [{name: unit, run: stored}]\n"
	for _, matrix := range []string{"[]", "null", "{alpha: null}", "{alpha: {env: {MODE: true}}}", "{alpha: {env: {MODE: 42}}}", "{alpha: {env: {MODE: null}}}", "{alpha: {image: null}}", "{alpha: {run: injected}}", "{alpha: {}, alpha: {}}", "{alpha: &v {}, beta: *v}"} {
		if _, err := Parse([]byte(prefix + "    matrix: " + matrix + "\n")); err == nil {
			t.Errorf("ambiguous matrix accepted: %s", matrix)
		}
	}
	def, err := Parse([]byte(prefix + "    matrix: {alpha: {env: {MODE: \"true\"}}, beta: {}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := Compile(def)
	if err != nil {
		t.Fatal(err)
	}
	req, _, _, _ := jobsBlobFixture(t)
	for _, job := range plan.Jobs {
		key := req.Key
		key.JobName = job.Name
		source := SourceArchive{Digest: "sha256:" + strings.Repeat("a", 64), Bytes: 1, Key: jobsObjectPrefix(key) + "source/" + strings.Repeat("a", 64) + ".tar.gz"}
		raw, err := jobsManifest(StartRequest{Key: key, Repository: plan.Repository, CommitSHA: req.CommitSHA, Job: job, Deadline: time.Now().Add(time.Minute)}, source)
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := DecodeJobsManifest(base64.StdEncoding.EncodeToString(raw))
		if err != nil || manifest.Job.Matrix != nil || bytes.Contains(raw, []byte(`"matrix"`)) {
			t.Fatal("unresolved matrix entered guest protocol")
		}
		want := 1
		if job.Name == "test-alpha" {
			want = 3
		}
		if manifest.Version != want {
			t.Fatal("matrix changed the concrete guest feature version")
		}
		manifest.Job.Matrix = map[string]MatrixVariant{"unexpected": {}}
		if ValidateJobsManifest(manifest) == nil {
			t.Fatal("guest accepted unexpanded matrix")
		}
		job.Matrix = manifest.Job.Matrix
		if _, err := jobsManifest(StartRequest{Job: job}, source); err == nil {
			t.Fatal("dispatch accepted an unexpanded job")
		}
	}
}
