package queuestarter

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func TestSourceIsExactShippedQueueWorker(t *testing.T) {
	files, err := Source()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	if err := fs.WalkDir(templates.FS, "queue-worker", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		original, err := fs.ReadFile(templates.FS, path)
		if err != nil {
			return err
		}
		if files[strings.TrimPrefix(path, "queue-worker/")] != string(original) {
			t.Fatal("acceptance changed shipped queue source")
		}
		count++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != len(files) {
		t.Fatal("acceptance added a test-only source file")
	}
	manifest, err := gregalemanifest.ParseBytes([]byte(files["gregale.yaml"]))
	if err != nil || manifest.Validate() != nil || len(manifest.QueueBindings) != 1 || manifest.QueueBindings[0].Name != "default" || manifest.QueueBindings[0].Mode != "push" {
		t.Fatal("queue acceptance lost shipped manifest contract")
	}
	request, err := BindingRequest()
	if err != nil || request.Name != "default" || request.QueueName != "default" || request.Mode != "push" || request.WorkloadClass != "http" || request.MaxConcurrency != 1 ||
		request.RetryPolicy == nil || request.RetryPolicy.MaxAttempts != 3 || request.RetryPolicy.BaseSeconds != 1 || request.RetryPolicy.MaxSeconds != 30 || request.RetryPolicy.JitterSeconds != 0.25 {
		t.Fatal("queue API request lost the shipped retry contract")
	}
	c := SuiteContract()
	if c.Template != "queue-worker" || c.Runtime != "node22" || len(c.Phases) != 9 || c.Phases[len(c.Phases)-1] != "independent_cleanup" {
		t.Fatal("queue acceptance matrix incomplete")
	}
}

func TestRuntimeManifestRequiresEveryQueueArtifactAndExactStarter(t *testing.T) {
	c := SuiteContract()
	m := RuntimeManifest{Version: c.Version, SourceRevision: strings.Repeat("a", 40), Template: c.Template, Runtime: c.Runtime, Artifacts: map[string]string{}}
	for _, name := range c.Artifacts {
		m.Artifacts[name] = strings.Repeat("b", 64)
	}
	if !m.Valid() {
		t.Fatal("complete queue bundle rejected")
	}
	for _, name := range c.Artifacts {
		digest := m.Artifacts[name]
		delete(m.Artifacts, name)
		if m.Valid() {
			t.Fatal("partial queue bundle accepted")
		}
		m.Artifacts[name] = digest
	}
	for _, change := range []func(*RuntimeManifest){func(m *RuntimeManifest) { m.Template = "event-worker" }, func(m *RuntimeManifest) { m.Runtime = "python312" }, func(m *RuntimeManifest) { m.Version = 0 }, func(m *RuntimeManifest) { m.SourceRevision = "dirty" }, func(m *RuntimeManifest) { m.Artifacts["foreign"] = strings.Repeat("c", 64) }} {
		candidate := m
		candidate.Artifacts = map[string]string{}
		for name, digest := range m.Artifacts {
			candidate.Artifacts[name] = digest
		}
		change(&candidate)
		if candidate.Valid() {
			t.Fatal("foreign queue bundle accepted")
		}
	}
}
