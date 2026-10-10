// adr: 947
package validatorbundle

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

type artifactMemory struct {
	objects map[string][]byte
	lose    bool
	writes  int
}

func (m *artifactMemory) Get(_ context.Context, key string, limit int64) ([]byte, string, error) {
	b, ok := m.objects[key]
	if !ok {
		return nil, "", durableentity.ErrNotFound
	}
	if int64(len(b)) > limit {
		return nil, "", durableentity.ErrLimit
	}
	return append([]byte(nil), b...), "version", nil
}
func (m *artifactMemory) Put(_ context.Context, key string, body []byte, expected string) (string, error) {
	m.writes++
	if expected != "" {
		return "", errors.New("overwrite attempted")
	}
	if _, ok := m.objects[key]; ok {
		return "", durableentity.ErrConflict
	}
	m.objects[key] = append([]byte(nil), body...)
	if m.lose {
		return "", errors.New("lost acknowledgement")
	}
	return "version", nil
}
func artifactFixture() (*Artifacts, *artifactMemory, Bundle) {
	b := Bundle{AppID: uuid.NewString(), DeploymentID: uuid.NewString(), Runtime: api.ExecutionRuntimeNode22, Entrypoint: "validator.mjs", Files: []api.ExecutionFile{{Path: "validator.mjs", Content: []byte("export default () => ({protocol_version:1,valid:true})")}}}
	b.SHA256 = Hash(b)
	memory := &artifactMemory{objects: map[string][]byte{}}
	return NewArtifacts(memory, map[string]bool{b.AppID: true}), memory, b
}

func TestArtifactsLostAcknowledgementAndIndependentReader(t *testing.T) {
	publisher, memory, b := artifactFixture()
	memory.lose = true
	if err := publisher.Publish(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	reader := NewArtifacts(memory, map[string]bool{b.AppID: true})
	got, err := reader.Resolve(t.Context(), b.AppID, b.DeploymentID)
	if err != nil || got.SHA256 != b.SHA256 {
		t.Fatal(got, err)
	}
	if err = publisher.Publish(t.Context(), b); err != nil {
		t.Fatal("identical retry failed", err)
	}
	changed := b
	changed.Files = []api.ExecutionFile{{Path: "validator.mjs", Content: []byte("changed")}}
	changed.SHA256 = Hash(changed)
	if err = publisher.Publish(t.Context(), changed); !errors.Is(err, ErrBindingConflict) {
		t.Fatal("deployment rebound", err)
	}
	got, err = reader.Resolve(t.Context(), b.AppID, b.DeploymentID)
	if err != nil || got.SHA256 != b.SHA256 {
		t.Fatal("original reference lost", got, err)
	}
}

func TestArtifactsMissingTamperedAndForeignScopeFailClosed(t *testing.T) {
	a, m, b := artifactFixture()
	if _, err := a.Resolve(t.Context(), b.AppID, b.DeploymentID); err == nil {
		t.Fatal("missing accepted")
	}
	if err := a.Publish(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	m.objects[artifactKey(b.AppID, b.SHA256)] = []byte(`{"runtime":"node22","entrypoint":"validator.mjs","files":[],"unknown":true}`)
	if _, err := a.Resolve(t.Context(), b.AppID, b.DeploymentID); err == nil {
		t.Fatal("tampering accepted")
	}
	if _, err := a.Resolve(t.Context(), uuid.NewString(), b.DeploymentID); err == nil {
		t.Fatal("foreign scope accepted")
	}
	if _, err := a.Resolve(t.Context(), b.AppID, "../escape"); err == nil {
		t.Fatal("invalid identity accepted")
	}
	var off *Artifacts
	if err := off.Check(t.Context(), b.AppID, b.DeploymentID); err != nil {
		t.Fatal("disabled gate blocked", err)
	}
	if err := a.Check(t.Context(), uuid.NewString(), uuid.NewString()); err != nil {
		t.Fatal("unrelated app blocked", err)
	}
}
