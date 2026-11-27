// adr: 20261109120000 — Tang peers named by host name bind at their private address.
package daemonunitspec

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// storageTangPeerTask returns the storage role's task that adds a Tang peer
// named by host name: its URL expression and its peer address expression.
func storageTangPeerTask(t *testing.T) (urlExpr, peerExpr string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy/ansible/roles/storage/tasks/encrypted.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var tasks []map[string]any
	if err := yaml.Unmarshal(body, &tasks); err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task["name"] != "storage — add each Tang peer named by host name at its private address" {
			continue
		}
		urlExpr, _ = task["ansible.builtin.set_fact"].(map[string]any)["faas_storage_tang_urls"].(string)
		peerExpr, _ = task["vars"].(map[string]any)["peer"].(string)
		return urlExpr, peerExpr
	}
	t.Fatal("encrypted.yml has no task adding Tang peers named by host name")
	return "", ""
}

// The bound URL is the peer's discovered private address (falling back to a
// provider inventory address), bracketed when IPv6, never its manifest name.
func TestStorageBindsNamedTangPeersAtTheirPrivateAddress(t *testing.T) {
	urlExpr, peerExpr := storageTangPeerTask(t)
	hostvars := map[string]any{
		"bhs-1": map[string]any{"faas_private_dns_address": "10.20.0.1"},
		"bhs-4": map[string]any{"faas_private_address": "10.20.0.4"},
		"v6":    map[string]any{"faas_private_dns_address": "fd00::7"},
	}
	for _, tc := range []struct{ item, want string }{
		{"bhs-1", "http://10.20.0.1:7500"},
		{"bhs-4", "http://10.20.0.4:7500"},
		{"v6", "http://[fd00::7]:7500"},
	} {
		peer := renderResolver(t, peerExpr, map[string]any{"hostvars": hostvars, "item": tc.item})
		got := renderResolver(t, urlExpr, map[string]any{"peer": peer, "faas_tang_port": 7500, "faas_storage_tang_urls": []string{}})
		if want := "['" + tc.want + "']"; got != want {
			t.Errorf("%s: URL list %s, want %s", tc.item, got, want)
		}
	}
}
