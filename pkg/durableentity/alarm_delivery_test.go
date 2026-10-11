// adr: 712
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAlarmRetryReservationsSurviveRestartAndExhaustWithoutConsumingState(t *testing.T) {
	f := newFixture(t)
	alarm := scheduleTestAlarm(t, f)
	for attempt := 1; attempt <= api.MaxDurableEntityAlarmAttempts; attempt++ {
		manager := openManager(t, f.store, f.clock)
		if _, err := manager.InvokeAlarm(t.Context(), alarm, "worker", func(context.Context, View) (Transition, error) {
			return Transition{}, errors.New("private handler error")
		}); err == nil {
			t.Fatal("failed handler acknowledged")
		}
		status, err := manager.InspectAlarm(t.Context(), f.id)
		if err != nil || status.Alarm == nil || status.Alarm.Version != 1 || status.Attempts != attempt || status.NextAttemptAt == nil || status.Exhausted != (attempt == api.MaxDurableEntityAlarmAttempts) {
			t.Fatal(status, err)
		}
		want := ErrAlarmBackoff
		if status.Exhausted {
			want = ErrAlarmExhausted
		}
		if _, err := manager.InvokeAlarm(t.Context(), alarm, "restart", func(context.Context, View) (Transition, error) {
			t.Fatal("blocked retry reached guest")
			return Transition{}, nil
		}); !errors.Is(err, want) {
			t.Fatal("retry budget was not enforced", err)
		}
		page, err := manager.ScanDueAlarms(t.Context(), "")
		if err != nil || len(page.Alarms) != 0 {
			t.Fatal("backed-off alarm was rediscovered", page, err)
		}
		f.clock.Store(status.NextAttemptAt.UnixNano())
	}
	assertCount(t, t.Context(), f.manager, f.id, 1, 1)
	// A deliberate business transition can clear or rearm an exhausted alarm.
	if _, err := f.manager.Invoke(t.Context(), f.id, "caller", request("rearm"), increment); err != nil {
		t.Fatal(err)
	}
	status, err := f.manager.InspectAlarm(t.Context(), f.id)
	if err != nil || status.Attempts != 0 || status.Exhausted || status.Alarm == nil || status.Alarm.Version != 2 {
		t.Fatal("new state did not reset retry identity", status, err)
	}
	if _, err := f.manager.InvokeAlarm(t.Context(), *status.Alarm, "worker", consumeTestAlarm); err != nil {
		t.Fatal(err)
	}
}

func TestAlarmReservationAcknowledgementLossAndRejection(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected", true: "accepted-response-lost"}[accepted], func(t *testing.T) {
			f := newFixture(t)
			alarm := scheduleTestAlarm(t, f)
			intercepted := false
			store := wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, version string) (string, error) {
				var value manifest
				if !intercepted && strings.HasSuffix(key, "/manifest.json") && json.Unmarshal(body, &value) == nil && value.AlarmDelivery != nil {
					intercepted = true
					if !accepted {
						return "", ErrConflict
					}
					if _, err := f.store.Put(ctx, key, body, version); err != nil {
						return "", err
					}
					return "", errors.New("reservation response lost")
				}
				return f.store.Put(ctx, key, body, version)
			}}
			manager := openManager(t, store, f.clock)
			_, err := manager.InvokeAlarm(t.Context(), alarm, "worker", func(context.Context, View) (Transition, error) {
				t.Fatal("uncertain/rejected reservation dispatched guest")
				return Transition{}, nil
			})
			want := ErrConflict
			if accepted {
				want = ErrUncertain
			}
			if !intercepted || !errors.Is(err, want) {
				t.Fatal(intercepted, err)
			}
			restarted := openManager(t, f.store, f.clock)
			status, err := restarted.InspectAlarm(t.Context(), f.id)
			if err != nil || status.Attempts != map[bool]int{false: 0, true: 1}[accepted] {
				t.Fatal(status, err)
			}
			if accepted {
				if _, err := restarted.InvokeAlarm(t.Context(), alarm, "restart", consumeTestAlarm); !errors.Is(err, ErrAlarmBackoff) {
					t.Fatal("accepted reservation vanished after restart", err)
				}
				f.clock.Store(status.NextAttemptAt.UnixNano())
			}
			if _, err := restarted.InvokeAlarm(t.Context(), alarm, "restart", consumeTestAlarm); err != nil {
				t.Fatal(err)
			}
			assertCount(t, t.Context(), restarted, f.id, 2, 2)
		})
	}
}

func TestAlarmRetryMetadataFailsClosedAndDoesNotLeakPayload(t *testing.T) {
	f := newFixture(t)
	alarm := scheduleTestAlarm(t, f)
	if _, err := f.manager.InvokeAlarm(t.Context(), alarm, "worker", func(context.Context, View) (Transition, error) {
		return Transition{}, errors.New("secret-provider-error")
	}); err == nil {
		t.Fatal("handler failure acknowledged")
	}
	status, err := f.manager.InspectAlarm(t.Context(), f.id)
	body, marshalErr := json.Marshal(status)
	if err != nil || marshalErr != nil || strings.Contains(string(body), "secret-provider-error") || strings.Contains(string(body), `"count"`) {
		t.Fatal("inspection exposed handler data", string(body), err, marshalErr)
	}
	base, etag, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"old-writer", "negative", "too-many", "wrong-deadline"} {
		t.Run(kind, func(t *testing.T) {
			value := base
			delivery := *base.AlarmDelivery
			value.AlarmDelivery = &delivery
			switch kind {
			case "old-writer":
				value.Schema = 3
			case "negative":
				delivery.Attempts = -1
			case "too-many":
				delivery.Attempts = api.MaxDurableEntityAlarmAttempts + 1
			case "wrong-deadline":
				delivery.At = alarm.At.Add(-time.Second)
			}
			body, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			next, err := f.store.Put(t.Context(), f.id.prefix()+"manifest.json", body, etag)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.manager.InspectAlarm(t.Context(), f.id); !errors.Is(err, ErrCorrupt) {
				t.Fatal("invalid retry authority accepted", err)
			}
			original, _ := json.Marshal(base)
			etag, err = f.store.Put(t.Context(), f.id.prefix()+"manifest.json", original, next)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
