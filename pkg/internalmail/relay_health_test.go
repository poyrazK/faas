package internalmail

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func healthyObservation(at time.Time) RelayObservation {
	return RelayObservation{ID: uuid.NewString(), ObservedAt: at, InventoryOK: true, SMTPUp: true}
}

func TestRelayBackpressureDefaultOffAndRequiresReports(t *testing.T) {
	for _, tc := range []struct{ master, flag string }{{"", "1"}, {"true", "1"}, {"1", ""}, {"1", "0"}, {"1", "true"}, {"1", "1 "}} {
		if BackpressureEnabled(func(key string) string {
			if key == EnableEnv {
				return tc.master
			}
			return tc.flag
		}) {
			t.Fatal("backpressure enabled without exact opt-in")
		}
	}
	s := &Service{Enabled: func() bool { return true }}
	if _, err := s.ReportRelayHealth(t.Context(), RelayObservation{}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := s.RelayStatus(t.Context()); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if err := s.DeliverOne(t.Context()); err != nil {
		t.Fatal("disabled workers accessed state", err)
	}
	s.BackpressureEnabled = func() bool { return true }
	if err := s.DeliverOne(t.Context()); !errors.Is(err, ErrInvalid) {
		t.Fatal("unfenced store accepted", err)
	}
	s.DeliveryEnabled = func() bool { return true }
	if err := s.DeliverOne(t.Context()); !errors.Is(err, ErrInvalid) {
		t.Fatal("unfenced dispatch accepted", err)
	}
	if err := s.beginMailSubmission(t.Context(), Recipient{}, ""); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	base := map[string]string{EnableEnv: "1", BackpressureEnableEnv: "1", "FAAS_INTERNAL_MAIL_TOKEN": testToken, "FAAS_INTERNAL_MAIL_AGE_IDENTITY_PATH": "/private/mail.age", "FAAS_INTERNAL_MAIL_SENDING_IP": "192.0.2.10"}
	get := func(key string) string { return base[key] }
	if _, err := LoadConfig(get); err == nil {
		t.Fatal("backpressure started without its collector credential")
	}
	base[ReportsEnableEnv], base["FAAS_INTERNAL_MAIL_REPORT_TOKEN"] = "1", reportToken
	if _, err := LoadConfig(get); err != nil {
		t.Fatal(err)
	}
}

func TestRelayRecoveryRequiresFreshContinuousObservations(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Microsecond)
	state := RelayStatus{}
	for elapsed := time.Duration(0); elapsed <= api.InternalMailRelayRecoveryWindow; elapsed += 15 * time.Second {
		at := start.Add(elapsed)
		state = nextRelayObservation(state, healthyObservation(at), at)
		if elapsed < api.InternalMailRelayRecoveryWindow && (!state.Held || state.Reason != "recovering") {
			t.Fatal("single healthy sample resumed delivery", state)
		}
	}
	if state.Held || state.State != "healthy" {
		t.Fatal("continuous recovery never resumed", state)
	}
	last := *state.Observation
	replay, err := applyRelayInput(state, &last, *state.ReceivedAt)
	if err != nil || !replay.ReceivedAt.Equal(*state.ReceivedAt) || !replay.HealthySince.Equal(*state.HealthySince) {
		t.Fatal("replay changed freshness", err)
	}
	staleAt := state.ReceivedAt.Add(api.InternalMailRelayMaxSampleAge)
	replay, err = applyRelayInput(state, &last, staleAt)
	if err != nil || !replay.Held || replay.Reason != "stale" || replay.HealthySince != nil {
		t.Fatal("replay revived a stale sample", err)
	}
	restarted := nextRelayObservation(state, healthyObservation(staleAt), staleAt)
	if !restarted.Held || restarted.Reason != "recovering" || !restarted.HealthySince.Equal(staleAt) {
		t.Fatal("missing samples preserved a recovery window")
	}
	changed := last
	changed.QueueMessages = 1
	if _, err := applyRelayInput(state, &changed, *state.ReceivedAt); !errors.Is(err, ErrConflict) {
		t.Fatal("contradictory replay accepted", err)
	}
}

func TestRelayFailureReasonsAndQueueAging(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name   string
		mutate func(*RelayObservation)
	}{
		{"inventory_unavailable", func(o *RelayObservation) { o.InventoryOK = false }},
		{"smtp_unavailable", func(o *RelayObservation) { o.SMTPUp = false }},
		{"queue_depth", func(o *RelayObservation) { o.QueueMessages = api.InternalMailRelayQueueHighWater }},
		{"queue_age", func(o *RelayObservation) {
			o.QueueMessages = 1
			o.OldestSeconds = int64(api.InternalMailRelayQueueMaxAge / time.Second)
		}},
	} {
		o := healthyObservation(now)
		tc.mutate(&o)
		state := nextRelayObservation(RelayStatus{}, o, now)
		if !state.Held || state.Reason != tc.name || state.HealthySince != nil {
			t.Fatal("bad relay did not hold", state)
		}
	}
	o := healthyObservation(now)
	o.QueueMessages, o.OldestSeconds = 1, int64(api.InternalMailRelayQueueMaxAge/time.Second)-1
	state := RelayStatus{State: "healthy", Reason: "healthy", Observation: &o, ReceivedAt: &now}
	if state.view(now).Held || state.view(now.Add(time.Second)).Reason != "queue_age" {
		t.Fatal("known queue age did not advance between samples")
	}
	for _, o := range []RelayObservation{
		{}, {ID: uuid.NewString(), ObservedAt: now, QueueMessages: -1},
		{ID: uuid.NewString(), ObservedAt: now, InventoryOK: true, OldestSeconds: 1},
		{ID: uuid.NewString(), ObservedAt: now, QueueMessages: 1},
	} {
		if _, err := o.normalized(); !errors.Is(err, ErrInvalid) {
			t.Fatal("malformed health accepted", err)
		}
	}
	for _, at := range []time.Time{now.Add(-api.InternalMailRelayMaxSampleAge), now.Add(api.InternalMailRelayClockSkew + time.Second)} {
		o := healthyObservation(at)
		if _, err := applyRelayInput(RelayStatus{}, &o, now); !errors.Is(err, ErrInvalid) {
			t.Fatal("stale or future sample accepted", err)
		}
	}
}
