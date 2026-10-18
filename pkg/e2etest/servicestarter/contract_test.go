package servicestarter

import (
	"strings"
	"testing"
)

func TestManifestRequiresTheEntireManagedServiceJourney(t *testing.T) {
	c := SuiteContract()
	m := RuntimeManifest{Version: c.Version, SourceRevision: strings.Repeat("a", 40), Template: c.Template, Runtime: c.Runtime, Artifacts: map[string]string{}}
	for _, name := range c.Artifacts {
		m.Artifacts[name] = strings.Repeat("b", 64)
	}
	if !m.Valid() {
		t.Fatal("complete service bundle rejected")
	}
	for _, name := range c.Artifacts {
		digest := m.Artifacts[name]
		delete(m.Artifacts, name)
		if m.Valid() {
			t.Fatalf("accepted missing artifact %s", name)
		}
		m.Artifacts[name] = digest
	}
	for _, change := range []func(*RuntimeManifest){func(m *RuntimeManifest) { m.Version = 0 }, func(m *RuntimeManifest) { m.SourceRevision = "dirty" }, func(m *RuntimeManifest) { m.Template = "queue-worker" }, func(m *RuntimeManifest) { m.Runtime = "node22" }, func(m *RuntimeManifest) { m.Artifacts["private_key"] = strings.Repeat("c", 64) }} {
		candidate := m
		candidate.Artifacts = map[string]string{}
		for name, digest := range m.Artifacts {
			candidate.Artifacts[name] = digest
		}
		change(&candidate)
		if candidate.Valid() {
			t.Fatal("accepted foreign runtime/source or private credential field")
		}
	}
}
