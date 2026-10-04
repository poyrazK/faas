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

// adr: 572
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
