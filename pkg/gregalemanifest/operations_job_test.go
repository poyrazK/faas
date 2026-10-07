// adr: 602
package gregalemanifest

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestOperationManifestBundlesJobTarget(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		parse        func([]byte) (*Manifest, error)
	}{
		{"yaml", "operations:\n  - name: export\n    job: export-job\n    method: POST\n    path: /exports\n    owner: platform_tenant\n    input_schema: input.json\n    output_schema: output.json\n    progress_stages: [generating]\n", ParseBytes},
		{"toml", "[[operations]]\nname='export'\njob='export-job'\nmethod='POST'\npath='/exports'\nowner='platform_tenant'\ninput_schema='input.json'\noutput_schema='output.json'\nprogress_stages=['generating']\n", ParseTOMLBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest, err := tc.parse([]byte(tc.source))
			if err != nil {
				t.Fatal(err)
			}
			if err := manifest.ResolveOperations("exports", api.PlanPro, func(string, int) ([]byte, error) { return []byte(`{"type":"object"}`), nil }); err != nil {
				t.Fatal(err)
			}
			if len(manifest.ResolvedOperations) != 1 || manifest.ResolvedOperations[0].Job != "export-job" || manifest.ResolvedOperations[0].Recovery != api.OperationRecoveryReconcile {
				t.Fatal("Job target lost during immutable bundling")
			}
		})
	}
}
