package outbound

import "context"

var _ AIFeedbackRecorder = (*MemoryBackend)(nil)

// RecordAIFeedback stores a rating of a logged call of the same caller
// (ADR 20261021171712).
func (b *MemoryBackend) RecordAIFeedback(ctx context.Context, feedback AIFeedback) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateAIFeedback(feedback); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[feedback.IntegrationID]
	if state == nil {
		return ErrAICallNotFound
	}
	for _, record := range state.aiRequestLog {
		if record.CallID == feedback.CallID && record.Caller == feedback.Caller {
			if state.feedback == nil {
				state.feedback = make(map[string]AIFeedback)
			}
			state.feedback[feedback.CallID] = feedback
			return nil
		}
	}
	return ErrAICallNotFound
}

// AIFeedbackFor reports a call's rating, for tests.
func (b *MemoryBackend) AIFeedbackFor(integrationID, callID string) (AIFeedback, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[integrationID]
	if state == nil {
		return AIFeedback{}, false
	}
	feedback, ok := state.feedback[callID]
	return feedback, ok
}

// AIPendingEvaluations lists the calls sampled for online evaluation, for
// tests.
func (b *MemoryBackend) AIPendingEvaluations(integrationID string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if state := b.states[integrationID]; state != nil {
		return append([]string(nil), state.pendingEvaluations...)
	}
	return nil
}
