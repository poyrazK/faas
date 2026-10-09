// ADR-521: customer operations preserve ownership, execution fences and independent delivery.
package gregalemanifest

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

func TestOperationManifestSourceBundle(t *testing.T) {
	for _, source := range []string{
		"operations:\n  - name: export\n    transaction_receipt: postgres_v1\n    method: POST\n    path: /exports\n    owner: platform_tenant\n    input_schema: schemas/input.json\n    output_schema: schemas/output.json\n    progress_stages: [generating, uploading]\n",
		"[[operations]]\nname = 'export'\ntransaction_receipt = 'postgres_v1'\nmethod = 'POST'\npath = '/exports'\nowner = 'platform_tenant'\ninput_schema = 'schemas/input.json'\noutput_schema = 'schemas/output.json'\nprogress_stages = ['generating', 'uploading']\n",
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
		if err != nil || reads != 2 || len(m.ResolvedOperations) != 1 || m.ResolvedOperations[0].Recovery != api.OperationRecoveryReconcile || m.ResolvedOperations[0].TransactionReceipt != api.OperationTransactionPostgres {
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

// ADR-664: native targets are named strings; malformed targets and stale
// source bundles must fail without partially replacing the resolved contract.
func TestOperationManifestMalformedNativeTargetAndAtomicResolution(t *testing.T) {
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
			t.Fatal("malformed native target entered manifest contract")
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

func TestOperationManifestWorkflowExport(t *testing.T) {
	raw, err := os.ReadFile("../../examples/customer-operation-workflow-export/gregale.yaml")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ParseBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.ResolveOperations("exports", api.PlanPro, func(file string, _ int) ([]byte, error) {
		return os.ReadFile("../../examples/customer-operation-workflow-export/" + file)
	}); err != nil {
		t.Fatal(err)
	}
	if len(manifest.ResolvedOperations) != 1 || manifest.ResolvedOperations[0].Workflow != "export-chain" || len(manifest.Workflows) != 1 {
		t.Fatalf("workflow contract lost: %+v", manifest.ResolvedOperations)
	}
	manifest.Workflows[0].Steps[1].DependsOn = nil
	if err := manifest.ResolveOperations("exports", api.PlanPro, func(string, int) ([]byte, error) { return []byte(`true`), nil }); err == nil || len(manifest.ResolvedOperations) != 0 {
		t.Fatal("parallel workflow entered the bounded adapter")
	}
}

// ADR-713: source declarations pin HTTP transaction negotiation into the revision.
func TestOperationManifestHTTPTransactionVersion(t *testing.T) {
	for _, format := range []string{"yaml", "toml"} {
		t.Run(format, func(t *testing.T) {
			var ordinary string
			for _, version := range []int{0, 1, 2, -1} {
				var source string
				var m *Manifest
				var err error
				if format == "yaml" {
					source = fmt.Sprintf("operations:\n  - name: fulfill\n    method: POST\n    path: /orders/fulfill\n    owner: platform_tenant\n    input_schema: input.json\n    output_schema: output.json\n    progress_stages: [complete]\n    http_transaction_version: %d\n", version)
					m, err = ParseBytes([]byte(source))
				} else {
					source = fmt.Sprintf("[[operations]]\nname='fulfill'\nmethod='POST'\npath='/orders/fulfill'\nowner='platform_tenant'\ninput_schema='input.json'\noutput_schema='output.json'\nprogress_stages=['complete']\nhttp_transaction_version=%d\n", version)
					m, err = ParseTOMLBytes([]byte(source))
				}
				if err != nil {
					t.Fatal(err)
				}
				reads := 0
				err = m.ResolveOperations("orders", api.PlanPro, func(string, int) ([]byte, error) { reads++; return []byte(`true`), nil })
				if version != 0 && version != 1 {
					if err == nil || reads != 0 || len(m.ResolvedOperations) != 0 {
						t.Fatalf("unsupported version %d read schemas or produced a bundle: %v", version, err)
					}
					continue
				}
				if err != nil || len(m.ResolvedOperations) != 1 || m.ResolvedOperations[0].HTTPTransactionVersion != version {
					t.Fatalf("version %d: %+v %v", version, m.ResolvedOperations, err)
				}
				contract, err := operations.Compile(m.ResolvedOperations[0], api.MustLimitsFor(api.PlanPro).Operations)
				if err != nil {
					t.Fatal(err)
				}
				if version == 0 {
					ordinary = contract.Revision
				} else if contract.Revision == ordinary {
					t.Fatal("negotiation change did not change the immutable revision")
				}
			}
		})
	}
}

func TestOperationWorkflowManifestPinsStepsAcrossOperations(t *testing.T) {
	source := `operations:
  - name: place-order
    method: POST
    path: /orders
    owner: platform_tenant
    input_schema: input.json
    output_schema: output.json
    progress_stages: [complete]
    milestones: {order-placed: placed.json}
    http_transaction_version: 1
  - name: authorize-payment
    method: POST
    path: /payments/authorize
    owner: platform_tenant
    input_schema: input.json
    output_schema: output.json
    progress_stages: [complete]
    milestones: {payment-authorized: paid.json}
    http_transaction_version: 1
  - name: fulfill-order
    method: POST
    path: /orders/fulfill
    owner: platform_tenant
    input_schema: input.json
    output_schema: output.json
    progress_stages: [complete]
    milestones: {order-fulfilled: fulfilled.json}
    http_transaction_version: 1
operation_workflows:
  - name: order-lifecycle
    title: Order lifecycle
    steps:
      - {name: placed, label: Order placed, operation: place-order, milestone: order-placed, instance_id_from: /workflow_run_id, position: 1}
      - {name: paid, label: Payment authorized, operation: authorize-payment, milestone: payment-authorized, instance_id_from: /workflow_run_id, position: 2}
      - {name: fulfilled, label: Order fulfilled, operation: fulfill-order, milestone: order-fulfilled, instance_id_from: /workflow_run_id, position: 3}
`
	manifest, err := ParseBytes([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	read := func(string, int) ([]byte, error) { reads++; return []byte(`true`), nil }
	if err := manifest.ResolveOperations("orders", api.PlanPro, read); err != nil {
		t.Fatal(err)
	}
	if reads != 9 || len(manifest.ResolvedOperations) != 3 {
		t.Fatalf("resolved %d schema files and %d operations", reads, len(manifest.ResolvedOperations))
	}
	revisions := map[string]string{}
	for _, spec := range manifest.ResolvedOperations {
		if len(spec.WorkflowSteps) != 1 || spec.WorkflowSteps[0].Workflow != "order-lifecycle" || spec.WorkflowSteps[0].Title != "Order lifecycle" {
			t.Fatalf("workflow step was not pinned to %s: %+v", spec.Name, spec.WorkflowSteps)
		}
		revisions[spec.Name] = mustWorkflowRevision(t, spec)
	}
	for i, j := 0, len(manifest.OperationWorkflows[0].Steps)-1; i < j; i, j = i+1, j-1 {
		manifest.OperationWorkflows[0].Steps[i], manifest.OperationWorkflows[0].Steps[j] = manifest.OperationWorkflows[0].Steps[j], manifest.OperationWorkflows[0].Steps[i]
	}
	if err := manifest.ResolveOperations("orders", api.PlanPro, read); err != nil {
		t.Fatal(err)
	}
	for _, spec := range manifest.ResolvedOperations {
		if mustWorkflowRevision(t, spec) != revisions[spec.Name] {
			t.Fatalf("workflow source order changed %s revision", spec.Name)
		}
	}
	manifest.OperationWorkflows[0].Steps[0].Operation = "unknown"
	reads = 0
	if err := manifest.ResolveOperations("orders", api.PlanPro, read); err == nil || reads != 0 || len(manifest.ResolvedOperations) != 0 {
		t.Fatalf("unknown workflow operation read schemas or retained stale bundle: reads=%d err=%v", reads, err)
	}
}

func mustWorkflowRevision(t *testing.T, spec api.OperationDefinitionSpec) string {
	t.Helper()
	contract, err := operations.Compile(spec, api.MustLimitsFor(api.PlanPro).Operations)
	if err != nil {
		t.Fatal(err)
	}
	return contract.Revision
}

// ADR-714: deployment bundles capture an independent business-reference declaration.
func TestOperationManifestSubjects(t *testing.T) {
	for _, source := range []string{
		"operations:\n  - name: fulfill-order\n    method: POST\n    path: /orders\n    owner: platform_tenant\n    input_schema: input.json\n    output_schema: output.json\n    progress_stages: [complete]\n    subject: {type: order, id_from: /order_id}\n",
		"[[operations]]\nname = 'fulfill-order'\nmethod = 'POST'\npath = '/orders'\nowner = 'platform_tenant'\ninput_schema = 'input.json'\noutput_schema = 'output.json'\nprogress_stages = ['complete']\n[operations.subject]\ntype = 'order'\nid_from = '/order_id'\n",
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
		if err := m.ResolveOperations("orders", api.PlanPro, func(string, int) ([]byte, error) { return []byte(`{"type":"object"}`), nil }); err != nil {
			t.Fatal(err)
		}
		subject := m.ResolvedOperations[0].Subject
		if subject == nil || subject.Type != "order" || subject.IDFrom != "/order_id" {
			t.Fatalf("source lost subject: %+v", subject)
		}
		m.Operations[0].Subject.Type = "changed"
		if subject.Type != "order" {
			t.Fatal("manifest mutation changed pinned subject")
		}
		m.Operations[0].Subject.IDFrom = "/~invalid"
		if err := m.ValidateForPlan(api.PlanPro); err == nil {
			t.Fatal("invalid JSON Pointer accepted")
		}
	}
}

func TestOperationMilestoneSourceBundle(t *testing.T) {
	for _, source := range []string{
		"operations:\n  - name: order\n    method: POST\n    path: /orders\n    owner: platform_tenant\n    input_schema: input.json\n    output_schema: output.json\n    progress_stages: [done]\n    http_transaction_version: 1\n    milestones: {paid: paid.json}\n",
		"[[operations]]\nname='order'\nmethod='POST'\npath='/orders'\nowner='platform_tenant'\ninput_schema='input.json'\noutput_schema='output.json'\nprogress_stages=['done']\nhttp_transaction_version=1\n[operations.milestones]\npaid='paid.json'\n",
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
		var reads []string
		if err := m.ResolveOperations("orders", api.PlanPro, func(file string, _ int) ([]byte, error) { reads = append(reads, file); return []byte(`true`), nil }); err != nil {
			t.Fatal(err)
		}
		if len(reads) != 3 || string(m.ResolvedOperations[0].Milestones["paid"]) != "true" {
			t.Fatal("milestone schema was not bundled")
		}
		if err := m.ResolveOperations("orders", api.PlanPro, func(file string, _ int) ([]byte, error) {
			if file == "paid.json" {
				return nil, errors.New("missing milestone schema")
			}
			return []byte(`true`), nil
		}); err == nil || len(m.ResolvedOperations) != 0 {
			t.Fatal("missing fact schema retained old resolution")
		}
		m.Operations[0].Milestones["paid"] = "../private.json"
		if err := m.ValidateForPlan(api.PlanPro); err == nil {
			t.Fatal("milestone schema escaped source boundary")
		}
	}
}
