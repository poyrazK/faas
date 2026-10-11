// ADR-521: customer operations preserve ownership, execution fences and independent delivery.
package gregalemanifest

import (
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestOperationManifestSourceBundle(t *testing.T) {
	for _, source := range []string{
		"operations:\n  - name: export\n    method: POST\n    path: /exports\n    owner: platform_tenant\n    input_schema: schemas/input.json\n    output_schema: schemas/output.json\n    progress_stages: [generating, uploading]\n",
		"[[operations]]\nname = 'export'\nmethod = 'POST'\npath = '/exports'\nowner = 'platform_tenant'\ninput_schema = 'schemas/input.json'\noutput_schema = 'schemas/output.json'\nprogress_stages = ['generating', 'uploading']\n",
	} {
		var m *Manifest
		var err error
		if source[0] == '[' {
			m, err = ParseTOMLBytes([]byte(source))
		} else {
			m, err = ParseBytes([]byte(source))
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := m.ValidateForPlan(api.PlanFree); err == nil {
			t.Fatal("free plan accepted operation definitions")
		}
		reads := 0
		err = m.ResolveOperations("exports", api.PlanPro, func(name string, limit int) ([]byte, error) {
			reads++
			if limit != api.MustLimitsFor(api.PlanPro).Operations.SchemaBytes {
				t.Fatal("schema read is not plan bounded")
			}
			switch name {
			case "schemas/input.json":
				return []byte(`{"type":"object","required":["count"],"properties":{"count":{"type":"integer"}},"additionalProperties":false}`), nil
			case "schemas/output.json":
				return []byte(`{"type":"object","required":["file"],"properties":{"file":{"type":"string"}}}`), nil
			default:
				return nil, errors.New("missing")
			}
		})
		if err != nil || reads != 2 || len(m.ResolvedOperations) != 1 || m.ResolvedOperations[0].Recovery != api.OperationRecoveryReconcile {
			t.Fatalf("immutable bundle: %+v, reads=%d, err=%v", m, reads, err)
		}
		m.Operations[0].InputSchema = "../outside.json"
		if err := m.ValidateForPlan(api.PlanPro); err == nil {
			t.Fatal("schema traversal accepted")
		}
	}
}

func TestOperationManifestPerAppContracts(t *testing.T) {
	m := &Manifest{}
	for _, app := range []string{"first", "second"} {
		for i := 0; i < api.MustLimitsFor(api.PlanHobby).Operations.DefinitionsPerApp; i++ {
			m.Operations = append(m.Operations, Operation{App: app, Name: fmt.Sprintf("export-%d", i), Method: "POST", Path: fmt.Sprintf("/exports/%d", i), Owner: api.OperationOwnerPlatformTenant, InputSchema: "input.json", OutputSchema: "output.json", ProgressStages: []string{"generating"}})
		}
	}
	read := func(string, int) ([]byte, error) { return []byte(`true`), nil }
	if err := m.ResolveOperations("first", api.PlanHobby, read); err != nil || len(m.ResolvedOperations) != 10 {
		t.Fatalf("per-app allowance: %d %v", len(m.ResolvedOperations), err)
	}
	m.Operations = []Operation{m.Operations[0], m.Operations[10]}
	m.Operations[1].App = ""
	if err := m.ResolveOperations("first", api.PlanHobby, read); err == nil {
		t.Fatal("common and app-specific declarations collided silently")
	}
}

// ADR-521: the staged HTTP contract rejects native targets and stale source bundles.
func TestOperationManifestHTTPOnlyAndAtomicResolution(t *testing.T) {
	for _, source := range []string{
		"operations:\n  - name: export\n    job: {name: export}\n",
		"[[operations]]\nname='export'\nworkflow={name='export'}\n",
	} {
		var err error
		if source[0] == '[' {
			_, err = ParseTOMLBytes([]byte(source))
		} else {
			_, err = ParseBytes([]byte(source))
		}
		if err == nil {
			t.Fatal("native target entered HTTP manifest contract")
		}
	}
	m := &Manifest{Operations: []Operation{{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, InputSchema: "input.json", OutputSchema: "output.json", ProgressStages: []string{"generating"}}}}
	if err := m.ResolveOperations("export", api.PlanPro, func(string, int) ([]byte, error) { return []byte(`true`), nil }); err != nil {
		t.Fatal(err)
	}
	if err := m.ResolveOperations("export", api.PlanPro, func(string, int) ([]byte, error) { return nil, errors.New("source changed") }); err == nil || len(m.ResolvedOperations) != 0 {
		t.Fatalf("old bundle retained on resolution failure: %+v %v", m.ResolvedOperations, err)
	}
}
