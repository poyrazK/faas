package main

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type protectionCLIClient struct {
	method, key, version, id string
	retention                api.ObjectVersionRetention
	hold                     api.ObjectVersionLegalHold
}

func (c *protectionCLIClient) GetObjectVersionRetention(_ context.Context, _, _, key, version string) (api.ObjectVersionRetentionResult, error) {
	c.method, c.key, c.version = "GET retention", key, version
	return api.ObjectVersionRetentionResult{}, nil
}
func (c *protectionCLIClient) GetObjectVersionLegalHold(_ context.Context, _, _, key, version string) (api.ObjectVersionLegalHoldResult, error) {
	c.method, c.key, c.version = "GET legal-hold", key, version
	return api.ObjectVersionLegalHoldResult{}, nil
}
func (c *protectionCLIClient) PutObjectVersionRetention(_ context.Context, _, _, key, version string, r api.ObjectVersionRetentionRequest) (api.ObjectVersionProtection, error) {
	c.method, c.key, c.version, c.id, c.retention = "PUT retention", key, version, r.ID, r.Retention
	return api.ObjectVersionProtection{}, nil
}
func (c *protectionCLIClient) PutObjectVersionLegalHold(_ context.Context, _, _, key, version string, r api.ObjectVersionLegalHoldRequest) (api.ObjectVersionProtection, error) {
	c.method, c.key, c.version, c.id, c.hold = "PUT legal-hold", key, version, r.ID, r.LegalHold
	return api.ObjectVersionProtection{}, nil
}
func (c *protectionCLIClient) GetObjectVersionProtection(_ context.Context, _, _, id string) (api.ObjectVersionProtection, error) {
	c.method, c.id = "GET status", id
	return api.ObjectVersionProtection{}, nil
}

// adr: 582
func TestVersionProtectionCLI(t *testing.T) {
	bucket, version, id := uuid.NewString(), uuid.NewString(), uuid.NewString()
	until := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	key := "目录 +%.txt"
	for _, tc := range []struct {
		args   []string
		method string
	}{
		{[]string{"status", "demo", bucket, id}, "GET status"},
		{[]string{"retention", "demo", bucket, key, version}, "GET retention"},
		{[]string{"retention", "demo", bucket, key, version, "COMPLIANCE", until, id}, "PUT retention"},
		{[]string{"retention", "demo", bucket, key, "null", "clear", id}, "PUT retention"},
		{[]string{"legal-hold", "demo", bucket, key, version}, "GET legal-hold"},
		{[]string{"legal-hold", "demo", bucket, key, version, "ON", id}, "PUT legal-hold"},
		{[]string{"legal-hold", "demo", bucket, key, "null", "OFF", id}, "PUT legal-hold"},
	} {
		c := &protectionCLIClient{}
		if _, err := runVersionProtection(t.Context(), c, tc.args); err != nil || c.method != tc.method {
			t.Fatal(tc, c, err)
		}
		if tc.args[0] == "status" {
			if c.id != id {
				t.Fatal(c)
			}
			continue
		}
		if c.key != key || c.version != tc.args[4] {
			t.Fatal("changed exact target", c)
		}
		if len(tc.args) > 5 && c.id != id {
			t.Fatal("lost retry identity", c)
		}
		if len(tc.args) == 8 && (c.retention.Mode != "COMPLIANCE" || c.retention.RetainUntilDate.Format(time.RFC3339Nano) != until) {
			t.Fatal(c)
		}
		if len(tc.args) == 7 && tc.args[5] == "clear" && !c.retention.Empty() {
			t.Fatal(c)
		}
		if tc.args[0] == "legal-hold" && len(tc.args) == 7 && c.hold.Status != tc.args[5] {
			t.Fatal(c)
		}
	}
	for _, args := range [][]string{
		{}, {"status", "demo", bucket, "null"},
		{"retention", "demo", bucket, key, ""},
		{"retention", "demo", bucket, key, version, "clear"},
		{"retention", "demo", bucket, key, version, "GOVERNANCE", "bad-time", id},
		{"retention", "demo", bucket, key, version, "GOVERNANCE", until, "null"},
		{"legal-hold", "demo", bucket, key, version, "true", id},
		{"legal-hold", "demo", bucket, key, version, "ON", id, "extra"},
	} {
		c := &protectionCLIClient{}
		if _, err := runVersionProtection(t.Context(), c, args); err == nil || c.method != "" {
			t.Fatal("invalid input contacted API", args, c, err)
		}
	}
}

// adr: 598
func TestEventHoldProtectionCLI(t *testing.T) {
	bucket, version, id := uuid.NewString(), uuid.NewString(), uuid.NewString()
	until := time.Now().UTC().AddDate(1, 0, 0).Format(time.RFC3339Nano)
	for _, tc := range []struct {
		args        []string
		status      string
		days, years int32
		minimum     bool
	}{
		{[]string{"event-hold", "demo", bucket, "key", version, "COMPLIANCE", "ON", "days", "30", id}, "ON", 30, 0, false},
		{[]string{"event-hold", "demo", bucket, "key", "null", "GOVERNANCE", "ON", "years", "1", "--retain-until", until, id}, "ON", 0, 1, true},
		{[]string{"event-hold", "demo", bucket, "key", version, "COMPLIANCE", "OFF", id}, "OFF", 0, 0, false},
		{[]string{"event-hold", "demo", bucket, "key", version, "COMPLIANCE", "OFF", "--retain-until", until, id}, "OFF", 0, 0, true},
	} {
		c := &protectionCLIClient{}
		if _, e := runVersionProtection(t.Context(), c, tc.args); e != nil || c.method != "PUT retention" || c.retention.EventHold != tc.status || c.id != id || (c.retention.RetainUntilDate != nil) != tc.minimum {
			t.Fatal(tc, c, e)
		}
		if tc.status == "ON" {
			p := c.retention.EventHoldDuration
			if p == nil || tc.days != 0 && (p.Days == nil || *p.Days != tc.days) || tc.years != 0 && (p.Years == nil || *p.Years != tc.years) {
				t.Fatal(c)
			}
		} else if c.retention.EventHoldDuration != nil {
			t.Fatal("release sent a duration", c)
		}
	}
	for _, tail := range [][]string{
		{"COMPLIANCE", "ON", "days", "0", id}, {"COMPLIANCE", "ON", "years", "101", id}, {"COMPLIANCE", "ON", "days", "36501", id},
		{"COMPLIANCE", "ON", "days", "1.5", id}, {"COMPLIANCE", "OFF", "days", "1", id}, {"COMPLIANCE", "OFF", "null"},
		{"COMPLIANCE", "ON", "days", "1", "--retain-until", "bad", id}, {"COMPLIANCE", "OFF", "extra", id},
	} {
		args := append([]string{"event-hold", "demo", bucket, "key", version}, tail...)
		c := &protectionCLIClient{}
		if _, e := runVersionProtection(t.Context(), c, args); e == nil || c.method != "" {
			t.Fatal("invalid event policy contacted API", args, e)
		}
	}
}
