package operations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

var operationName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Contract is compiled once per immutable definition revision. Compiled JSON
// schemas are safe for concurrent input/output validation.
type Contract struct {
	Spec       api.OperationDefinitionSpec
	Revision   string
	Input      *jsonschema.Schema
	Output     *jsonschema.Schema
	Milestones map[string]*jsonschema.Schema
}

func Compile(spec api.OperationDefinitionSpec, limits api.OperationPlanLimits) (*Contract, error) {
	if !limits.Allowed {
		return nil, fmt.Errorf("operations are not enabled for this plan")
	}
	if len(spec.Name) == 0 || len(spec.Name) > api.OperationNameMaxBytes || !operationName.MatchString(spec.Name) {
		return nil, fmt.Errorf("operation name must be a bounded lowercase slug")
	}
	if spec.Method != "POST" && spec.Method != "PUT" && spec.Method != "PATCH" && spec.Method != "DELETE" {
		return nil, fmt.Errorf("operation method must be POST, PUT, PATCH, or DELETE")
	}
	if spec.Workflow != "" && (len(spec.Workflow) > api.OperationNameMaxBytes || !operationName.MatchString(spec.Workflow) || spec.Method != "POST" || spec.Recovery == api.OperationRecoverySafeRetry) {
		return nil, fmt.Errorf("workflow operations require a bounded workflow name, POST ingress and reconciliation recovery")
	}
	if spec.Job != "" && (spec.Workflow != "" || len(spec.Job) > api.OperationNameMaxBytes || !operationName.MatchString(spec.Job) || spec.Method != "POST" || spec.Recovery == api.OperationRecoverySafeRetry) {
		return nil, fmt.Errorf("job operations require a bounded job name, POST ingress and reconciliation recovery")
	}
	u, err := url.ParseRequestURI(spec.Path)
	if err != nil || !strings.HasPrefix(spec.Path, "/") || strings.HasPrefix(spec.Path, "//") || len(spec.Path) > api.OperationPathMaxBytes || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("operation path must be an absolute application path without a query")
	}
	if spec.Owner != api.OperationOwnerPlatformTenant {
		return nil, fmt.Errorf("operation owner must be platform_tenant")
	}
	if spec.Subject != nil {
		if err := validateSubjectSpec(*spec.Subject); err != nil {
			return nil, err
		}
		subject := *spec.Subject
		spec.Subject = &subject
	}
	if spec.HTTPTransactionVersion != 0 && spec.HTTPTransactionVersion != api.OperationHTTPTransactionVersion {
		return nil, fmt.Errorf("unsupported HTTP operation transaction version")
	}
	if spec.Recovery == "" {
		spec.Recovery = api.OperationRecoveryReconcile
	}
	if spec.Recovery != api.OperationRecoveryReconcile && spec.Recovery != api.OperationRecoverySafeRetry {
		return nil, fmt.Errorf("unsupported operation recovery policy")
	}
	if spec.TransactionReceipt != "" && (spec.TransactionReceipt != api.OperationTransactionPostgres || spec.Workflow != "" || spec.Job != "" || spec.Recovery != api.OperationRecoveryReconcile) {
		return nil, fmt.Errorf("transaction receipts require postgres_v1, an ordinary HTTP handler and reconciliation recovery")
	}
	if len(spec.ProgressStages) == 0 || len(spec.ProgressStages) > limits.ProgressStages {
		return nil, fmt.Errorf("operation progress stages exceed plan limit or are empty")
	}
	seen := map[string]bool{}
	for _, stage := range spec.ProgressStages {
		if len(stage) > api.OperationNameMaxBytes || !operationName.MatchString(stage) || seen[stage] {
			return nil, fmt.Errorf("operation progress stages must be unique bounded slugs")
		}
		seen[stage] = true
	}
	input, canonicalInput, err := compileSchema(spec.InputSchema, limits.SchemaBytes)
	if err != nil {
		return nil, fmt.Errorf("operation input schema: %w", err)
	}
	output, canonicalOutput, err := compileSchema(spec.OutputSchema, limits.SchemaBytes)
	if err != nil {
		return nil, fmt.Errorf("operation output schema: %w", err)
	}
	spec.InputSchema, spec.OutputSchema = canonicalInput, canonicalOutput
	milestones, err := compileMilestones(&spec, limits)
	if err != nil {
		return nil, err
	}
	if err := compileWorkflowSteps(&spec); err != nil {
		return nil, err
	}
	spec.ProgressStages = append([]string(nil), spec.ProgressStages...)
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("encode operation definition: %w", err)
	}
	revision, err := InputFingerprint(raw)
	if err != nil {
		return nil, err
	}
	return &Contract{Spec: spec, Revision: revision, Input: input, Output: output, Milestones: milestones}, nil
}

type closedSchemaLoader struct{}

func (closedSchemaLoader) Load(string) (any, error) {
	return nil, fmt.Errorf("operation schemas must be bundled; external resources are forbidden")
}

func compileSchema(raw json.RawMessage, maxBytes int) (*jsonschema.Schema, json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maxBytes {
		return nil, nil, fmt.Errorf("schema is empty or exceeds plan limit")
	}
	canonical, err := CanonicalJSON(raw)
	if err != nil {
		return nil, nil, err
	}
	if len(canonical) > maxBytes {
		return nil, nil, fmt.Errorf("canonical schema exceeds plan limit")
	}
	var doc any
	d := json.NewDecoder(bytes.NewReader(canonical))
	d.UseNumber()
	if err := d.Decode(&doc); err != nil {
		return nil, nil, err
	}
	if err := validateSchemaReferences(doc); err != nil {
		return nil, nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(closedSchemaLoader{})
	const location = "urn:gregale:operation-schema"
	if err := c.AddResource(location, doc); err != nil {
		return nil, nil, err
	}
	schema, err := c.Compile(location)
	return schema, canonical, err
}

// Traverse schema positions only: const/default/enum/examples and property
// names can contain ordinary customer data whose keys happen to be "$ref".
// The closed loader independently rejects any resource fetched by the compiler.
func validateSchemaReferences(value any) error {
	v, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"$ref", "$dynamicRef", "$recursiveRef", "$id"} {
		if ref, ok := v[key].(string); ok && ref != "" && !strings.HasPrefix(ref, "#") {
			return fmt.Errorf("schema resources must use local fragment references")
		}
	}
	for key, child := range v {
		switch key {
		case "$defs", "definitions", "properties", "patternProperties", "dependentSchemas", "dependencies":
			if schemas, ok := child.(map[string]any); ok {
				for _, schema := range schemas {
					if err := validateSchemaReferences(schema); err != nil {
						return err
					}
				}
			}
		case "allOf", "anyOf", "oneOf", "prefixItems":
			if schemas, ok := child.([]any); ok {
				for _, schema := range schemas {
					if err := validateSchemaReferences(schema); err != nil {
						return err
					}
				}
			}
		case "items":
			if schemas, ok := child.([]any); ok {
				for _, schema := range schemas {
					if err := validateSchemaReferences(schema); err != nil {
						return err
					}
				}
			} else if err := validateSchemaReferences(child); err != nil {
				return err
			}
		case "additionalProperties", "additionalItems", "unevaluatedProperties", "unevaluatedItems", "contains", "contentSchema", "propertyNames", "not", "if", "then", "else":
			if err := validateSchemaReferences(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateValue(schema *jsonschema.Schema, raw json.RawMessage, maxBytes int) error {
	if len(raw) == 0 || len(raw) > maxBytes {
		return fmt.Errorf("operation value is empty or exceeds plan limit")
	}
	canonical, err := CanonicalJSON(raw)
	if err != nil {
		return err
	}
	if len(canonical) > maxBytes {
		return fmt.Errorf("canonical operation value exceeds plan limit")
	}
	d := json.NewDecoder(bytes.NewReader(canonical))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return err
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("operation value does not match its schema: %w", err)
	}
	return nil
}

func (c *Contract) ValidateInput(raw json.RawMessage, maxBytes int) error {
	return validateValue(c.Input, raw, maxBytes)
}

func (c *Contract) ValidateOutput(raw json.RawMessage, maxBytes int) error {
	return validateValue(c.Output, raw, maxBytes)
}

func (c *Contract) ValidateProgress(report api.OperationReportRequest) error {
	if len(report.ReportID) == 0 || len(report.ReportID) > api.OperationReportIDMaxBytes || strings.ContainsAny(report.ReportID, "\x00\r\n") {
		return fmt.Errorf("operation report_id must be a bounded stable identifier")
	}
	if report.Completed < 0 || report.Total < 1 || report.Completed > report.Total {
		return fmt.Errorf("operation progress must satisfy 0 <= completed <= total")
	}
	for _, stage := range c.Spec.ProgressStages {
		if report.Stage == stage {
			return nil
		}
	}
	return fmt.Errorf("operation progress stage is not declared")
}
