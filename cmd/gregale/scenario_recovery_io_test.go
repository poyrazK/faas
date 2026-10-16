package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func fastRecoveryContext() context.Context {
	return context.WithValue(context.Background(), testCampaignRecoveryPolicyKey{}, testCampaignRecoveryPolicy{Timeout: 50 * time.Millisecond, Backoff: time.Millisecond, MaxBackoff: 20 * time.Millisecond, Attempts: 3})
}
func recoveryUnavailable() error {
	return &api.APIError{Problem: api.Problem{Status: 503, Code: "unavailable", Detail: "secret-must-not-be-persisted"}}
}

func TestCampaignFaultRecoveryReadBudget(t *testing.T) {
	// adr: 984 — transient reads recover within a bounded, cancellable budget.
	t.Run("recovers-with-backoff", func(t *testing.T) {
		calls := 0
		started := time.Now()
		got, err := testCampaignRecoveryRead(fastRecoveryContext(), func(context.Context) (string, error) {
			calls++
			if calls < 3 {
				return "", recoveryUnavailable()
			}
			return "recovered", nil
		})
		if err != nil || got != "recovered" || calls != 3 || time.Since(started) < 3*time.Millisecond {
			t.Fatal(got, err, calls)
		}
	})
	for _, status := range []int{401, 403, 404, 409} {
		t.Run(httpStatusName(status), func(t *testing.T) {
			calls := 0
			_, err := testCampaignRecoveryRead(fastRecoveryContext(), func(context.Context) (int, error) {
				calls++
				return 0, &api.APIError{Problem: api.Problem{Status: status, Code: "guard"}}
			})
			if err == nil || calls != 1 {
				t.Fatal("retried a guard failure", calls, err)
			}
		})
	}
	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(fastRecoveryContext(), 10*time.Millisecond)
		defer cancel()
		started := time.Now()
		_, err := testCampaignRecoveryRead(ctx, func(c context.Context) (int, error) { <-c.Done(); return 0, c.Err() })
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
			t.Fatal("lookup exceeded operation deadline", err)
		}
	})
	t.Run("cancel-backoff", func(t *testing.T) {
		ctx, cancel := context.WithCancel(fastRecoveryContext())
		defer cancel()
		calls := 0
		_, err := testCampaignRecoveryRead(ctx, func(context.Context) (int, error) { calls++; cancel(); return 0, recoveryUnavailable() })
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatal("cancellation continued retries", err, calls)
		}
	})
	t.Run("retry-after", func(t *testing.T) {
		calls := 0
		seconds := int64(60)
		_, err := testCampaignRecoveryRead(fastRecoveryContext(), func(context.Context) (int, error) {
			calls++
			return 0, &api.APIError{Problem: api.Problem{Status: 429, Code: "rate_limit", RetryAfterSeconds: &seconds}}
		})
		if err == nil || calls != 1 {
			t.Fatal("retried before server retry-after", calls, err)
		}
	})
}
func httpStatusName(status int) string {
	switch status {
	case 401:
		return "unauthorized"
	case 403:
		return "forbidden"
	case 404:
		return "not-found"
	default:
		return "conflict"
	}
}

type recoveryOutageHistoryClient struct {
	*campaignFaultHistoryAPI
	outage     bool
	loseRevoke bool
	reads      int
}

func (c *recoveryOutageHistoryClient) GetAPIConsumer(ctx context.Context, slug, id string) (api.APIConsumerResponse, error) {
	c.reads++
	if c.outage {
		return api.APIConsumerResponse{}, recoveryUnavailable()
	}
	return c.campaignFaultHistoryAPI.GetAPIConsumer(ctx, slug, id)
}
func (c *recoveryOutageHistoryClient) RevokeAPIConsumer(ctx context.Context, slug, id string) (api.APIConsumerResponse, error) {
	v, err := c.campaignFaultHistoryAPI.RevokeAPIConsumer(ctx, slug, id)
	if err == nil && c.loseRevoke {
		c.loseRevoke = false
		return api.APIConsumerResponse{}, recoveryUnavailable()
	}
	return v, err
}

func TestCampaignFaultRecoveryOutageResume(t *testing.T) {
	// adr: 984 — outages preserve unresolved inventory and resume from live proofs.
	for _, mode := range []string{"lookup-outage", "committed-revocation"} {
		t.Run(mode, func(t *testing.T) {
			f := newCampaignFaultHistoryFixture(t)
			c := &recoveryOutageHistoryClient{campaignFaultHistoryAPI: newCampaignFaultHistoryAPI(t, f), outage: mode == "lookup-outage", loseRevoke: mode == "committed-revocation"}
			dirs := []string{}
			for _, dir := range f.dirs {
				dirs = append(dirs, testCampaignJournalDirectory(dir, "a"))
			}
			frozen := campaignFaultFrozenBytes(t, dirs)
			first, err := cleanupTestCampaignFaultHistory(fastRecoveryContext(), c, f.plan, f.cp, f.scenarios, f.dirs[2], nil, true)
			if err == nil || first.Complete || first.Issue == nil || len(first.Faults) != 3 || len(first.Sessions) != 3 {
				t.Fatal("uncertainty was not retained", first, err)
			}
			if mode == "lookup-outage" && (c.reads != 3 || c.revokes != 0 || c.deletes != 0 || first.Faults[0].Consumers["customer"] != "unverified" || first.Faults[0].Issues[0].Attempts != 3) {
				t.Fatal("unbounded or mutating outage preflight", first)
			}
			if mode == "committed-revocation" && c.revokes != 1 {
				t.Fatal("mutation retried after lost response", c.revokes)
			}
			files, err := filepath.Glob(filepath.Join(f.dirs[2], "fault-history-cleanup-*.json"))
			if err != nil || len(files) != 1 {
				t.Fatal(files, err)
			}
			raw, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "secret-must-not-be-persisted") {
				t.Fatal("raw failure detail persisted")
			}
			var saved testCampaignFaultHistory
			if json.Unmarshal(raw, &saved) != nil || saved.Complete || saved.Issue == nil {
				t.Fatal("failure observation was not persisted")
			}
			c.outage = false
			for i := 0; i < 2; i++ {
				result, err := cleanupTestCampaignFaultHistory(fastRecoveryContext(), c, f.plan, f.cp, f.scenarios, f.dirs[2], nil, true)
				if err != nil || !result.Complete {
					t.Fatal("cleanup did not resume", err)
				}
			}
			if c.revokes != 3 || c.deletes != 3 || c.namespaces != 3 {
				t.Fatal("resumption duplicated mutations", c.revokes, c.deletes, c.namespaces)
			}
			assertCampaignFaultFrozen(t, frozen, dirs)
		})
	}
}
