package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type objectLockCLIClient struct {
	method        string
	configuration api.ObjectBucketObjectLockConfiguration
}

func (c *objectLockCLIClient) GetObjectBucketObjectLock(context.Context, string, string) (api.ObjectBucketObjectLock, error) {
	c.method = "GET"
	return api.ObjectBucketObjectLock{}, nil
}
func (c *objectLockCLIClient) GetObjectBucketObjectLockCapabilities(context.Context, string, string) (api.ObjectLockCapabilities, error) {
	c.method = "CAPS"
	return api.ObjectLockCapabilities{}, nil
}
func (c *objectLockCLIClient) PutObjectBucketObjectLock(_ context.Context, _, _ string, v api.ObjectBucketObjectLockConfiguration) (api.ObjectBucketObjectLock, error) {
	c.method = "PUT"
	c.configuration = v
	return api.ObjectBucketObjectLock{EnabledRequired: true}, nil
}

// adr: 564
func TestBucketObjectLockCLI(t *testing.T) {
	for _, tc := range []struct {
		args         []string
		method       string
		fixed, event bool
	}{
		{[]string{"status", "demo", "bucket"}, "GET", false, false},
		{[]string{"capabilities", "demo", "bucket"}, "CAPS", false, false},
		{[]string{"enable", "demo", "bucket"}, "PUT", false, false},
		{[]string{"clear-default", "demo", "bucket"}, "PUT", false, false},
		{[]string{"GOVERNANCE", "demo", "bucket", "--days", "3"}, "PUT", true, false},
		{[]string{"COMPLIANCE", "demo", "bucket", "--years", "2", "--event-days", "5"}, "PUT", true, true},
		{[]string{"COMPLIANCE", "demo", "bucket", "--event-years", "1"}, "PUT", false, true},
	} {
		c := &objectLockCLIClient{}
		if _, err := runBucketObjectLock(t.Context(), c, tc.args); err != nil || c.method != tc.method {
			t.Fatal(tc, c, err)
		}
		if tc.method != "PUT" {
			continue
		}
		v := c.configuration
		if !v.Enabled || !v.Valid() {
			t.Fatal(v)
		}
		if tc.fixed || tc.event {
			if v.DefaultRetention == nil || (v.DefaultRetention.Days != nil || v.DefaultRetention.Years != nil) != tc.fixed || (v.DefaultRetention.DefaultEventHold != nil) != tc.event {
				t.Fatal(tc, v)
			}
		} else if v.DefaultRetention != nil {
			t.Fatal("clear retained a default", v)
		}
	}
	for _, args := range [][]string{
		{}, {"disable", "demo", "bucket"}, {"enable", "demo", "bucket", "extra"},
		{"GOVERNANCE", "demo", "bucket"}, {"GOVERNANCE", "demo", "bucket", "--days", "0"},
		{"GOVERNANCE", "demo", "bucket", "--days", "1", "--days", "2"},
		{"COMPLIANCE", "demo", "bucket", "--days", "1", "--years", "1"},
		{"COMPLIANCE", "demo", "bucket", "--event-days", "1", "--event-years", "1"},
		{"COMPLIANCE", "demo", "bucket", "--days", "2147483648"},
		{"COMPLIANCE", "demo", "bucket", "--unknown", "1"},
	} {
		c := &objectLockCLIClient{}
		if _, err := runBucketObjectLock(t.Context(), c, args); err == nil || c.method != "" {
			t.Fatal("invalid args contacted service", args, c, err)
		}
	}
}
