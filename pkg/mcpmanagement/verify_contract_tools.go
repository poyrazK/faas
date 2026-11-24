package mcpmanagement

// ADR-20261122220000: verification contracts for agents. An agent validates
// a gregale.verify.yaml, reviews a change as a plan pinned to the stored
// revision, and applies it with gregale_apply_operation like any other
// operation. The contract then gates every new deployment of the app.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/verifycontract"
)

type verifyContractArgs struct {
	YAML     string         `json:"yaml,omitempty" jsonschema:"gregale.verify.yaml text; set yaml or contract"`
	Contract map[string]any `json:"contract,omitempty" jsonschema:"The contract as a JSON object; set yaml or contract"`
}

type planVerifyContractArgs struct {
	App      string         `json:"app" jsonschema:"Existing app slug"`
	Op       string         `json:"op" jsonschema:"set or delete"`
	YAML     string         `json:"yaml,omitempty" jsonschema:"gregale.verify.yaml text for set"`
	Contract map[string]any `json:"contract,omitempty" jsonschema:"The contract as a JSON object for set"`
	Reason   string         `json:"reason" jsonschema:"Reviewable reason; at most 280 characters"`
}

// verifyContractChange is the stored plan request.
type verifyContractChange struct {
	Op               string          `json:"op"`
	Contract         json.RawMessage `json:"contract,omitempty"`
	ExpectedRevision int64           `json:"expected_revision"`
}

func registerVerificationContracts(s *mcp.Server, m *management) {
	register(s, m, "gregale_validate_verification_contract", "Validate a gregale.verify.yaml (HTTP, MCP and read-only SQL checks run against each new deployment before it takes traffic) and list its checks, which are required or advisory, and which run only on previews and environment branches. Changes nothing and calls no app.", m.validateVerificationContract)
	register(s, m, "gregale_get_verification_contract", "Read the app's stored verification contract and its revision; contract is null when the app has none. Read-only.", m.getVerificationContract)
	register(s, m, "gregale_plan_verification_contract_change", "Review setting or deleting the app's verification contract. The plan is pinned to the stored revision. Changes nothing; apply with gregale_apply_operation. Once set, a new deployment whose required check fails is not promoted.", m.planVerificationContractChange)
}

func parseVerifyContractArg(yamlText string, object map[string]any) (verifycontract.Contract, error) {
	switch {
	case yamlText != "" && object != nil:
		return verifycontract.Contract{}, invalid("invalid_argument", "set yaml or contract, not both")
	case yamlText != "":
		c, err := verifycontract.ParseYAML([]byte(yamlText))
		if err != nil {
			return c, invalid("invalid_contract", err.Error())
		}
		return c, nil
	case object != nil:
		raw, err := json.Marshal(object)
		if err != nil {
			return verifycontract.Contract{}, invalid("invalid_argument", "contract is not JSON")
		}
		c, err := verifycontract.ParseJSON(raw)
		if err != nil {
			return c, invalid("invalid_contract", err.Error())
		}
		return c, nil
	}
	return verifycontract.Contract{}, invalid("invalid_argument", "set yaml or contract")
}

func verifyContractSummary(c verifycontract.Contract) map[string]any {
	checks := make([]map[string]any, 0, len(c.Checks))
	for _, check := range c.Checks {
		checks = append(checks, map[string]any{"name": check.Name, "kind": check.Kind(), "required": !check.Advisory, "previews_and_branches_only": check.Mutating()})
	}
	_, sum, _ := c.Canonical()
	return map[string]any{"valid": true, "contract_sha256": sum, "timeout_seconds": c.Timeout(), "checks": checks,
		"note": "In production, MCP tool checks run only when the tool declares readOnlyHint: true; others are skipped like state-changing HTTP checks."}
}

func (m *management) validateVerificationContract(_ context.Context, a verifyContractArgs) (any, error) {
	c, err := parseVerifyContractArg(a.YAML, a.Contract)
	if err != nil {
		return nil, err
	}
	return verifyContractSummary(c), nil
}

func (m *management) getVerificationContract(ctx context.Context, a appArgs) (any, error) {
	if _, err := m.consentedApp(ctx, a.App, false); err != nil {
		return nil, err
	}
	stored, err := m.client.GetVerificationContract(ctx, a.App)
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusNotFound {
		return map[string]any{"app": a.App, "contract": nil, "revision": 0}, nil
	}
	if err != nil {
		return nil, err
	}
	return stored, nil
}

func (m *management) planVerificationContractChange(ctx context.Context, a planVerifyContractArgs) (any, error) {
	if _, err := m.consentedApp(ctx, a.App, false); err != nil {
		return nil, err
	}
	current, err := m.getVerificationContract(ctx, appArgs{App: a.App})
	if err != nil {
		return nil, err
	}
	req := verifyContractChange{Op: a.Op}
	view := map[string]any{}
	if stored, ok := current.(api.VerificationContract); ok {
		req.ExpectedRevision = stored.Revision
		view["before"] = map[string]any{"revision": stored.Revision, "contract_sha256": stored.ContractSHA256, "check_count": stored.CheckCount}
	}
	effect := ""
	switch a.Op {
	case "set":
		c, err := parseVerifyContractArg(a.YAML, a.Contract)
		if err != nil {
			return nil, err
		}
		raw, _, err := c.Canonical()
		if err != nil {
			return nil, invalid("invalid_contract", err.Error())
		}
		req.Contract = raw
		view["after"] = verifyContractSummary(c)
		effect = "From the next deployment on, every required check must pass before the deployment takes traffic; a failure leaves the previous deployment serving."
	case "delete":
		if req.ExpectedRevision == 0 {
			return nil, invalid("invalid_argument", a.App+" has no verification contract to delete")
		}
		effect = "New deployments are no longer checked against a verification contract."
	default:
		return nil, invalid("invalid_argument", "op must be set or delete")
	}
	p, err := m.operationBase(ctx, operationArgs{App: a.App, Reason: a.Reason}, "verify_contract_change", req)
	if err != nil {
		return nil, err
	}
	p.Operation.Changes, _ = json.Marshal(view)
	p.TrafficEffect = effect
	return m.savePlan(p)
}

// runVerifyContractChange applies a verification contract plan. The
// revision pin makes a concurrent change fail instead of being overwritten.
func (m *management) runVerifyContractChange(ctx context.Context, p *releasePlan) (any, error) {
	var req verifyContractChange
	if err := json.Unmarshal(p.Operation.Request, &req); err != nil {
		return nil, err
	}
	switch req.Op {
	case "set":
		expected := req.ExpectedRevision
		return m.client.PutVerificationContract(ctx, p.App, api.PutVerificationContractRequest{Contract: req.Contract, ExpectedRevision: &expected})
	case "delete":
		// The revision is checked by the delete itself, so a contract set
		// after this plan was reviewed is never removed.
		expected := req.ExpectedRevision
		if err := m.client.DeleteVerificationContract(ctx, p.App, &expected); err != nil {
			var apiErr *api.APIError
			if errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusConflict {
				return nil, invalid("plan_stale", "the verification contract changed after this plan; plan again")
			}
			return nil, err
		}
		return map[string]any{"app": p.App, "deleted": true}, nil
	}
	return nil, invalid("invalid_plan", "unsupported verification contract change")
}
