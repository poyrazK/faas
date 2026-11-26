package mcphosting

import (
	"context"
	"encoding/json"
	"errors"
)

// Verification contract outcomes (ADR-20261122220000).
var (
	// ErrContractIncompatible means the server failed initialization or
	// catalog listing, so no fixture could run.
	ErrContractIncompatible = errors.New("MCP server did not initialize or list its catalog")
	// ErrContractToolNotFound means the catalog has no tool of that name.
	ErrContractToolNotFound = errors.New("MCP tool is not in the server catalog")
	// ErrContractToolNotReadOnly means a production verification asked for a
	// tool that does not declare readOnlyHint: true.
	ErrContractToolNotReadOnly = errors.New("MCP tool does not declare readOnlyHint, so it runs only on previews and environment branches")
	// ErrContractCaseFailed means the operation failed or its result did not
	// match the fixture.
	ErrContractCaseFailed = errors.New("MCP operation failed or its result did not match")
)

// RunContractCase runs one verification-contract fixture against client.
// With requireReadOnlyTool, a tool fixture runs only when the tool's
// annotations declare readOnlyHint: true. The hint is the customer's own
// declaration about their own tool; it decides whether the platform may call
// the tool in production, not whether an agent may trust it.
func RunContractCase(ctx context.Context, client *Client, fixture SmokeCase, requireReadOnlyTool bool) error {
	if err := (SmokeSuite{Version: 1, Cases: []SmokeCase{fixture}}).Validate(); err != nil {
		return err
	}
	validation := ValidateCompatibility(ctx, client, nil)
	if !validation.OK {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrContractIncompatible
	}
	if fixture.Kind == "tool" {
		var tool *Tool
		for i := range validation.Catalog.Tools {
			if validation.Catalog.Tools[i].Name == fixture.Target {
				tool = &validation.Catalog.Tools[i]
				break
			}
		}
		if tool == nil {
			return ErrContractToolNotFound
		}
		if requireReadOnlyTool && !declaresReadOnly(tool.Annotations) {
			return ErrContractToolNotReadOnly
		}
	}
	if !runSmokeCase(ctx, client, validation.Catalog, fixture) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrContractCaseFailed
	}
	return nil
}

func declaresReadOnly(annotations json.RawMessage) bool {
	var hints struct {
		ReadOnlyHint bool `json:"readOnlyHint"`
	}
	return len(annotations) > 0 && json.Unmarshal(annotations, &hints) == nil && hints.ReadOnlyHint
}
