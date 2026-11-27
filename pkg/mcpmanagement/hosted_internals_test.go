package mcpmanagement

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// Hosted plans showed the bridge's loopback apid address and its upload
// directory on the state disk. Output names the public origin and a
// placeholder instead; a local server's output is unchanged.
func TestHostedOutputHidesBridgeInternals(t *testing.T) {
	root := filepath.Join(t.TempDir(), "uploads")
	store, err := newUploadStore(root, "https://api.gregale.test/mcp")
	if err != nil {
		t.Fatal(err)
	}
	hosted := &management{opts: Options{APIBase: "http://127.0.0.1:8081", LogsAPIBase: "https://api.gregale.test", uploads: store}}
	out, err := hosted.safeOutput(map[string]any{"api_base": "http://127.0.0.1:8081", "path": root + "/plans/abc", "note": "see http://127.0.0.1:8081/v1/apps"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out.(json.RawMessage))
	if strings.Contains(text, "127.0.0.1") || strings.Contains(text, root) || !strings.Contains(text, `"api_base":"https://api.gregale.test"`) || !strings.Contains(text, "[hosted-upload]/plans/abc") {
		t.Fatalf("hosted output=%s", text)
	}
	local := &management{opts: Options{APIBase: "http://127.0.0.1:8081"}}
	out, err = local.safeOutput(map[string]any{"api_base": "http://127.0.0.1:8081"})
	if err != nil || !strings.Contains(string(out.(json.RawMessage)), "127.0.0.1:8081") {
		t.Fatalf("local output=%v %v", out, err)
	}
}
