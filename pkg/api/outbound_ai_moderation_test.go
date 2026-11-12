package api

import "testing"

// ADR 20261027132936 amendment 2: a blocking check without an on_error
// fails closed; a flagging one lets calls through.
func TestOutboundAIModerationOnErrorDefault(t *testing.T) {
	for action, want := range map[string]string{
		OutboundAIModerationBlock: OutboundAIModerationOnErrorBlock,
		OutboundAIModerationFlag:  OutboundAIModerationOnErrorAllow,
		OutboundAIModerationOff:   OutboundAIModerationOnErrorAllow,
		"":                        OutboundAIModerationOnErrorAllow,
	} {
		if got := (OutboundAIModeration{Action: action}).Normalized().OnError; got != want {
			t.Errorf("action %q: on_error = %q, want %q", action, got, want)
		}
	}
	explicit := OutboundAIModeration{Action: OutboundAIModerationBlock, OnError: OutboundAIModerationOnErrorAllow}
	if got := explicit.Normalized().OnError; got != OutboundAIModerationOnErrorAllow {
		t.Errorf("an explicit on_error was replaced: %q", got)
	}
}
