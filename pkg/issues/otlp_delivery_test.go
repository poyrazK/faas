package issues

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestDeliverOTLPGroupsIsolatesPermanentFailuresAndRetries(t *testing.T) {
	permanentErr := errors.New("permanent")
	transientErr := errors.New("temporary")
	stored := map[string]bool{}
	transient := true
	deliver := func(_ context.Context, items []string) ([]string, error) {
		for _, item := range items {
			if item == "bad" {
				return nil, permanentErr
			}
			if item == "later" && transient {
				return nil, transientErr
			}
		}
		for _, item := range items {
			stored[item] = true
		}
		return items, nil
	}
	groups := [][]string{{"first"}, {"bad", "same-span"}, {"later"}}
	permanent := func(err error) bool { return errors.Is(err, permanentErr) }
	var observed []error
	observe := func(err error) { observed = append(observed, err) }
	got, rejected, err := DeliverOTLPGroups(context.Background(), groups, deliver, permanent, func(error) bool { return false }, observe)
	if !errors.Is(err, transientErr) || rejected != 1 || !reflect.DeepEqual(got, []string{"first"}) || stored["same-span"] {
		t.Fatalf("first attempt: accepted %v rejected %d stored %v err %v", got, rejected, stored, err)
	}
	transient = false
	got, rejected, err = DeliverOTLPGroups(context.Background(), groups, deliver, permanent, func(error) bool { return false }, observe)
	if err != nil || rejected != 1 || !reflect.DeepEqual(got, []string{"first", "later"}) || len(stored) != 2 || len(observed) != 2 || !errors.Is(observed[0], permanentErr) {
		t.Fatalf("retry: accepted %v rejected %d stored %v err %v", got, rejected, stored, err)
	}
}

func TestDeliverOTLPGroupsMakesProgressAcrossRateWindows(t *testing.T) {
	quota := errors.New("rate limit")
	stored := map[string]bool{}
	remaining := 1
	deliver := func(_ context.Context, items []string) ([]string, error) {
		newItems := 0
		for _, item := range items {
			if !stored[item] {
				newItems++
			}
		}
		if newItems > remaining {
			return nil, quota
		}
		for _, item := range items {
			if !stored[item] {
				remaining--
				stored[item] = true
			}
		}
		return items, nil
	}
	groups := [][]string{{"one"}, {"two"}, {"three"}}
	for window := 1; window <= 3; window++ {
		remaining = 1
		_, rejected, err := DeliverOTLPGroups(context.Background(), groups, deliver, func(error) bool { return false }, func(err error) bool { return errors.Is(err, quota) }, nil)
		if rejected != 0 || len(stored) != window || (window < 3 && !errors.Is(err, quota)) || (window == 3 && err != nil) {
			t.Fatalf("window %d: stored=%v rejected=%d err=%v", window, stored, rejected, err)
		}
	}
}

func TestDeliverOTLPGroupsIsolatesRetryableSourceMaps(t *testing.T) {
	unavailable := errors.New("source maps unavailable")
	stored := map[string]bool{}
	deliver := func(_ context.Context, items []string) ([]string, error) {
		for _, item := range items {
			if item == "mapped" {
				return nil, unavailable
			}
		}
		for _, item := range items {
			stored[item] = true
		}
		return items, nil
	}
	got, rejected, err := DeliverOTLPGroups(context.Background(), [][]string{{"mapped"}, {"plain"}}, deliver, func(error) bool { return false }, func(err error) bool { return errors.Is(err, unavailable) }, nil)
	if !errors.Is(err, unavailable) || rejected != 0 || !reflect.DeepEqual(got, []string{"plain"}) || !stored["plain"] {
		t.Fatalf("accepted=%v rejected=%d stored=%v err=%v", got, rejected, stored, err)
	}
}

type batchAlreadyFullError struct{}

func (batchAlreadyFullError) Error() string                     { return "rate limit" }
func (batchAlreadyFullError) IssueBatchNoCapacityAtStart() bool { return true }

func TestDeliverOTLPGroupsDoesNotAmplifyAnExhaustedQuota(t *testing.T) {
	groups := make([][]int, 32)
	for i := range groups {
		groups[i] = []int{i}
	}
	calls := 0
	_, rejected, err := DeliverOTLPGroups(context.Background(), groups, func(context.Context, []int) ([]int, error) {
		calls++
		return nil, batchAlreadyFullError{}
	}, func(error) bool { return false }, func(error) bool { return true }, nil)
	if err == nil || rejected != 0 || calls != 1 {
		t.Fatalf("calls=%d rejected=%d err=%v", calls, rejected, err)
	}
}
