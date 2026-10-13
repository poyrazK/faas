package outbound

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// AIGatewayCapability is the AI gateway's product capability ID.
const AIGatewayCapability = "ai-gateway"

// AIGatewayRollout limits the AI gateway to listed accounts during a staged
// preview rollout (ADR-998). A nil rollout admits every entitled account.
type AIGatewayRollout map[string]struct{}

// ParseAIGatewayRollout reads the configured account list, or the
// comma-separated FAAS_AI_GATEWAY_ACCOUNTS override when it is set. An empty
// list or "*" admits every entitled account; any other entry must be an
// account UUID.
func ParseAIGatewayRollout(configured []string, override string) (AIGatewayRollout, error) {
	entries := configured
	if strings.TrimSpace(override) != "" {
		entries = strings.Split(override, ",")
	}
	var rollout AIGatewayRollout
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		switch entry {
		case "":
			continue
		case "*":
			return nil, nil
		}
		id, err := uuid.Parse(entry)
		if err != nil {
			return nil, fmt.Errorf("AI gateway rollout account %q is not a UUID", entry)
		}
		if rollout == nil {
			rollout = AIGatewayRollout{}
		}
		rollout[id.String()] = struct{}{}
	}
	return rollout, nil
}

// Allows reports whether the rollout admits an account.
func (r AIGatewayRollout) Allows(accountID string) bool {
	if r == nil {
		return true
	}
	id, err := uuid.Parse(accountID)
	if err != nil {
		return false
	}
	_, ok := r[id.String()]
	return ok
}

// AIGatewayEntitled reports whether an account plan includes the AI gateway
// in the capability registry (ADR-998).
func AIGatewayEntitled(plan api.Plan) bool {
	entitled, err := api.CapabilityEntitled(AIGatewayCapability, plan)
	return err == nil && entitled
}
