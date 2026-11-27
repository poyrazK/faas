package fcvm

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
)

func TestExecutionTemplateCannotAcceptAnExecution(t *testing.T) {
	m := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{}, "1.7.0", nil, nil)
	m.live["template"] = &Instance{ExecutionOnly: true, executionTemplateOnly: true}
	_, err := m.ExecuteExecution(t.Context(), "template", executionproto.Request{Version: executionproto.Version, ExecutionID: "template", Runtime: api.ExecutionRuntimeNode24, Source: "private", Input: json.RawMessage(`null`), TimeoutMS: 1000, MaxOutput: 1024, NetworkMode: api.ExecutionNetworkNone})
	if !errors.Is(err, ErrExecutionInstanceNotFound) {
		t.Fatalf("template accepted execution payload: %v", err)
	}
}
