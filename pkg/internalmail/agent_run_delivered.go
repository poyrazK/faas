package internalmail

import "strings"

// agentRunDeliveredMessage reports an agent run summary (ADR-20261027090010 §8). It
// goes only to the account's own address, which apid's dispatch rechecks
// under the account lock, so maild needs no source read at submission.
func agentRunDeliveredMessage(m Message) bool {
	return m.ProducerID != "" && strings.HasPrefix(m.IdempotencyKey, "agent-run-delivered:")
}
