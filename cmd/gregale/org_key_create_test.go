package main_test

import (
	"encoding/json"
	"net/http"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// `gregale key create <label>` names the label positionally while the org
// command took only --label. Both spellings now work; naming two different
// labels is refused rather than guessed.
func TestOrgKeyCreateTakesAPositionalLabel(t *testing.T) {
	for name, tc := range map[string]struct {
		args  []string
		label string
		code  int
	}{
		"positional":      {args: []string{"ci-agent", "--org", "acme"}, label: "ci-agent"},
		"flag":            {args: []string{"--org", "acme", "--label", "ci-agent"}, label: "ci-agent"},
		"both agree":      {args: []string{"ci-agent", "--org", "acme", "--label", "ci-agent"}, label: "ci-agent"},
		"both disagree":   {args: []string{"ci-agent", "--org", "acme", "--label", "other"}, code: 1},
		"two positionals": {args: []string{"ci", "agent", "--org", "acme"}, code: 1},
	} {
		t.Run(name, func(t *testing.T) {
			main.ResetJSONOut(t)
			resp, _ := json.Marshal(api.APIKeyResponse{ID: "k-1", Prefix: "fp_live_abc", Label: tc.label, Plaintext: "DO-NOT-LEAK"})
			f := main.AuthedFakeAPI(t, string(resp), http.StatusOK)
			if code := main.CmdOrgsKeysAdd(tc.args); code != tc.code {
				t.Fatalf("exit = %d, want %d", code, tc.code)
			}
			if tc.code != 0 {
				if *f.SawMethod() == http.MethodPost {
					t.Fatal("refused arguments still created a key")
				}
				return
			}
			var body struct {
				Label string `json:"label"`
			}
			if err := json.Unmarshal(*f.SawBody(), &body); err != nil || body.Label != tc.label || *f.SawPath() != "/v1/orgs/acme/keys" {
				t.Fatalf("request %s body=%s", *f.SawPath(), *f.SawBody())
			}
		})
	}
}
