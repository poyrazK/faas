package main_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// Hunt 33 H33-51: a node22 function's deploy receipt printed
// `profile: node (port 3000)` and `start_command: node handler.js`, which
// describe a container the function does not run in.
func TestFunctionDeployReceiptLeavesOutTheContainerProfile(t *testing.T) {
	raw := json.RawMessage(`{"schema_version":1,"deployment_id":"d1","app_id":"a1","source":{"kind":"tarball"},"profile":{"version":"1","framework":"node","port":3000,"health_path":"/healthz","start_command":"node handler.js"},"artifact":{},"smoke":{"status":"verified","path":"/healthz","status_code":200}}`)
	for _, function := range []bool{false, true} {
		var out bytes.Buffer
		main.RenderDeploymentHostingReceiptFor(&out, raw, function)
		text := out.String()
		hasProfile := strings.Contains(text, "profile:") || strings.Contains(text, "start_command:")
		if hasProfile == function || !strings.Contains(text, "health_path:") {
			t.Errorf("function=%t receipt:\n%s", function, text)
		}
	}
}
