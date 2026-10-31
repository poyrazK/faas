package api

import "fmt"

// A broker trigger may target an agent instead of its app's function
// (ADR-20261027090010 §9): schedd admits one agent run per record, and the record
// succeeds once its run is admitted. Agent targets are a preview behind the
// agents capability.

// triggerAgentKinds are the kinds that may target an agent: the external
// brokers, whose records carry a message identity.
var triggerAgentKinds = map[TriggerKind]bool{
	TriggerKindKafka: true, TriggerKindNATS: true, TriggerKindRedisStreams: true, TriggerKindSQSCompat: true,
}

// ValidateTriggerAgent checks that a trigger of kind and delivery may target
// the named agent.
func ValidateTriggerAgent(kind TriggerKind, delivery, agent string) error {
	switch {
	case !workflowAgentName.MatchString(agent):
		return fmt.Errorf("agent must name an agent: 1-%d characters of a-z, 0-9 and '-', starting with a letter", AgentNameMaxLen)
	case !triggerAgentKinds[kind]:
		return fmt.Errorf("agent targets are available for kafka, nats, redis_streams and sqs_compat triggers, not %s", kind)
	case delivery != "" && delivery != TriggerDeliveryRecord:
		return fmt.Errorf("an agent trigger starts one run per record; use delivery %q", TriggerDeliveryRecord)
	}
	return nil
}
