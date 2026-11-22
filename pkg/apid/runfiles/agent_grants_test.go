package runfiles_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runfiles"
	"github.com/onebox-faas/faas/pkg/runfile"
	"github.com/onebox-faas/faas/pkg/state"
)

type grantFileReader struct {
	file  state.ExecutionFile
	reads int
}

func (r *grantFileReader) ExecutionFileByID(_ context.Context, account, id string) (state.ExecutionFile, error) {
	r.reads++
	if r.file.ID != id || r.file.AccountID != account {
		return state.ExecutionFile{}, state.ErrNotFound
	}
	return r.file, nil
}

func TestAgentFileGrantBoundToOneCallAndUploadGeneration(t *testing.T) {
	now := time.Now().UTC()
	scope := runfiles.AgentFileScope{AccountID: uuid.NewString(), EndpointID: uuid.NewString(), ThreadID: "report",
		ToolCallID: "assistant:upload-1", TaskName: "summarize", Version: 2}
	file := state.ExecutionFile{ID: uuid.NewString(), AccountID: scope.AccountID, Kind: "upload", Status: "ready", ExpiresAt: now.Add(8 * time.Minute)}
	file.StorageKey = runfile.Key(file.AccountID, file.ID, uuid.NewString())
	reader := &grantFileReader{file: file}
	key := bytes.Repeat([]byte{7}, 32)
	signer, err := runfiles.NewAgentFileSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := signer.Issue(file, scope, strings.Repeat("&", api.ExecutionBundleMaxPathSize), now)
	if err != nil || !grant.ExpiresAt.Equal(file.ExpiresAt) {
		t.Fatalf("issue expiring grant: %v", err)
	}
	fresh, err := runfiles.NewAgentFileSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	in, err := fresh.Resolve(t.Context(), reader, scope, grant.GrantToken, now)
	if err != nil || in.FileID != file.ID || in.Path != strings.Repeat("&", api.ExecutionBundleMaxPathSize) || in.GrantToken != "" {
		t.Fatal("fresh signer did not resolve the pinned staged input", err)
	}

	refuse := func(scope runfiles.AgentFileScope, token string, stamp time.Time) {
		t.Helper()
		_, err := fresh.Resolve(t.Context(), reader, scope, token, stamp)
		var p *api.Problem
		if !errors.As(err, &p) || p.Status != http.StatusNotFound || strings.Contains(p.Detail, token) {
			t.Fatal("invalid scope or grant did not fail closed")
		}
	}
	for name, change := range map[string]func(*runfiles.AgentFileScope){
		"account":  func(s *runfiles.AgentFileScope) { s.AccountID = uuid.NewString() },
		"endpoint": func(s *runfiles.AgentFileScope) { s.EndpointID = uuid.NewString() },
		"thread":   func(s *runfiles.AgentFileScope) { s.ThreadID += "-other" },
		"call":     func(s *runfiles.AgentFileScope) { s.ToolCallID += "-other" },
		"task":     func(s *runfiles.AgentFileScope) { s.TaskName += "-other" },
		"version":  func(s *runfiles.AgentFileScope) { s.Version++ },
		"step":     func(s *runfiles.AgentFileScope) { s.StepLabel = "csv-report" },
	} {
		t.Run(name, func(t *testing.T) {
			altered := scope
			change(&altered)
			refuse(altered, grant.GrantToken, now)
		})
	}
	changed := byte('A')
	if grant.GrantToken[5] == changed {
		changed = 'B'
	}
	refuse(scope, "agf1."+string(changed)+grant.GrantToken[6:], now)
	refuse(scope, grant.GrantToken, file.ExpiresAt)
	if reader.reads != 1 {
		t.Fatal("invalid grants reached the file lookup")
	}
	reader.file.Status = "deleting"
	refuse(scope, grant.GrantToken, now)
	reader.file.Status = "ready"
	reader.file.StorageKey = runfile.Key(file.AccountID, file.ID, uuid.NewString())
	refuse(scope, grant.GrantToken, now)

	if _, err := signer.Issue(file, scope, "../escape", now); err == nil {
		t.Fatal("issued grant for an unsafe staged path")
	}
	file.Kind = "artifact"
	if _, err := signer.Issue(file, scope, "orders.json", now); err == nil {
		t.Fatal("issued an upload grant for a Run output")
	}
}
