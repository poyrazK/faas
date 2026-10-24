package sched_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// settleLog records Nacks and dead-letter writes in the order they happen.
type settleLog struct {
	calls    []string
	failNack map[string]bool
}

type settlePoller struct{ log *settleLog }

func (settlePoller) Kind() string                                        { return "queue" }
func (settlePoller) Poll(context.Context, sqlc.Trigger) sched.PollResult { return sched.PollResult{} }
func (settlePoller) Ack(context.Context, sqlc.Trigger, []string) error   { return nil }
func (settlePoller) Close() error                                        { return nil }
func (p settlePoller) Nack(_ context.Context, _ sqlc.Trigger, ids []string, reason string) error {
	if p.log.failNack[reason] {
		p.log.calls = append(p.log.calls, fmt.Sprintf("nack failed %s %v", reason, ids))
		return errors.New("claim lease expired")
	}
	p.log.calls = append(p.log.calls, fmt.Sprintf("nack %s %v", reason, ids))
	return nil
}

type settleStore struct {
	*sched.FakeDeadLetterStore
	log *settleLog
}

func (s settleStore) InsertTriggerDeadLetter(_ context.Context, recordID, _, reason, _ string, _ []byte) error {
	s.log.calls = append(s.log.calls, "dead-letter "+recordID+" "+reason)
	return nil
}

func (s settleStore) MarkTriggerRecordDeadLetter(context.Context, string, string) error { return nil }

// A queue receipt dead-letters only after its invocation did: the invocation
// is the message's one unified dead-letter entry (ADR-20261022203653), so a
// failed Nack must leave the receipt out of the DLQ. Brokers keep writing the
// receipt first, because the receipt is their durable record.
func TestSettleTriggerDeadLettersOrdersQueueNackFirst(t *testing.T) {
	const r1, r2 = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	cases := []struct {
		name     string
		queue    bool
		failNack []string
		want     []string
	}{
		{"queue nacks before the receipt", true, nil, []string{
			"nack poison_record [i1]", "nack max_attempts [i2]",
			"dead-letter " + r1 + " poison_record", "dead-letter " + r2 + " max_attempts"}},
		{"queue nack failure keeps that receipt out", true, []string{"poison_record"}, []string{
			"nack failed poison_record [i1]", "nack max_attempts [i2]",
			"dead-letter " + r2 + " max_attempts"}},
		{"queue nack failures keep every receipt out", true, []string{"poison_record", "max_attempts"}, []string{
			"nack failed poison_record [i1]", "nack failed max_attempts [i2]"}},
		{"broker writes the receipt before the nack", false, nil, []string{
			"dead-letter " + r1 + " poison_record", "dead-letter " + r2 + " max_attempts",
			"nack poison_record [i1]", "nack max_attempts [i2]"}},
		{"broker nack failure keeps the written receipt", false, []string{"poison_record"}, []string{
			"dead-letter " + r1 + " poison_record", "dead-letter " + r2 + " max_attempts",
			"nack failed poison_record [i1]", "nack max_attempts [i2]"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := &settleLog{failNack: map[string]bool{}}
			for _, reason := range tc.failNack {
				log.failNack[reason] = true
			}
			l := sched.NewLoop(nil, nil, discardLog())
			l.SettleTriggerDeadLetters(context.Background(), sqlc.Trigger{Kind: "queue"}, settlePoller{log},
				settleStore{FakeDeadLetterStore: &sched.FakeDeadLetterStore{}, log: log}, tc.queue,
				[]string{r1, r2}, []string{"i1", "i2"}, []string{"poison_record", "max_attempts"},
				[]string{"handler crashed", "retries exhausted"}, []int32{1, 3})
			if !slices.Equal(log.calls, tc.want) {
				t.Fatalf("calls:\n%q\nwant:\n%q", log.calls, tc.want)
			}
		})
	}
}
