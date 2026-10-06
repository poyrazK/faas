package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type bucketObjectLockClient interface {
	GetObjectBucketObjectLock(context.Context, string, string) (api.ObjectBucketObjectLock, error)
	GetObjectBucketObjectLockCapabilities(context.Context, string, string) (api.ObjectLockCapabilities, error)
	PutObjectBucketObjectLock(context.Context, string, string, api.ObjectBucketObjectLockConfiguration) (api.ObjectBucketObjectLock, error)
}

func cmdBucketObjectLock(args []string) int {
	if len(args) < 3 || !api.ValidAppSlug(args[1]) {
		return bucketObjectLockUsage()
	}
	if _, err := uuid.Parse(args[2]); err != nil {
		return bucketObjectLockUsage()
	}
	if _, err := bucketObjectLockSelection(args); err != nil {
		return bucketObjectLockUsage()
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := runBucketObjectLock(context.Background(), c, args)
	if err != nil {
		return printErr("Could not configure bucket Object Lock", err)
	}
	return jsonOut(writeJSON(out))
}

func runBucketObjectLock(ctx context.Context, c bucketObjectLockClient, args []string) (any, error) {
	configuration, err := bucketObjectLockSelection(args)
	if err != nil {
		return nil, err
	}
	switch args[0] {
	case "status":
		return c.GetObjectBucketObjectLock(ctx, args[1], args[2])
	case "capabilities":
		return c.GetObjectBucketObjectLockCapabilities(ctx, args[1], args[2])
	default:
		return c.PutObjectBucketObjectLock(ctx, args[1], args[2], configuration)
	}
}

func bucketObjectLockSelection(args []string) (api.ObjectBucketObjectLockConfiguration, error) {
	c := api.ObjectBucketObjectLockConfiguration{Enabled: true}
	if len(args) < 3 {
		return c, fmt.Errorf("missing Object Lock arguments")
	}
	switch args[0] {
	case "status", "capabilities", "enable", "clear-default":
		if len(args) == 3 {
			return c, nil
		}
	case "GOVERNANCE", "COMPLIANCE":
		if len(args) < 5 || (len(args)-3)%2 != 0 {
			break
		}
		d := &api.ObjectLockDefaultRetention{Mode: args[0]}
		seen := map[string]bool{}
		for i := 3; i < len(args); i += 2 {
			value, err := strconv.ParseInt(args[i+1], 10, 32)
			if err != nil || value <= 0 || seen[args[i]] {
				return c, fmt.Errorf("invalid retention duration")
			}
			seen[args[i]] = true
			n := int32(value)
			switch args[i] {
			case "--days":
				d.Days = &n
			case "--years":
				d.Years = &n
			case "--event-days", "--event-years":
				if d.DefaultEventHold == nil {
					d.DefaultEventHold = &api.ObjectRetentionPeriod{}
				}
				if args[i] == "--event-days" {
					d.DefaultEventHold.Days = &n
				} else {
					d.DefaultEventHold.Years = &n
				}
			default:
				return c, fmt.Errorf("unknown Object Lock option")
			}
		}
		c.DefaultRetention = d
		if c.Valid() {
			return c, nil
		}
	}
	return c, fmt.Errorf("invalid Object Lock arguments")
}

func bucketObjectLockUsage() int {
	PrintUsage(osStderr, "usage: gregale bucket object-lock <status|capabilities|enable|clear-default|GOVERNANCE|COMPLIANCE> <app> <bucket-id> [--days N | --years N] [--event-days N | --event-years N]", "bucket")
	return 1
}

func objectLockCLIFlags() []cliFlag {
	return []cliFlag{
		{Name: "days", Short: "fixed retention in days", Value: "N"},
		{Name: "years", Short: "fixed retention in years", Value: "N"},
		{Name: "event-days", Short: "event hold duration in days", Value: "N"},
		{Name: "event-years", Short: "event hold duration in years", Value: "N"},
	}
}
