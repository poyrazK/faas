package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type versionProtectionClient interface {
	GetObjectVersionRetention(context.Context, string, string, string, string) (api.ObjectVersionRetentionResult, error)
	PutObjectVersionRetention(context.Context, string, string, string, string, api.ObjectVersionRetentionRequest) (api.ObjectVersionProtection, error)
	GetObjectVersionLegalHold(context.Context, string, string, string, string) (api.ObjectVersionLegalHoldResult, error)
	PutObjectVersionLegalHold(context.Context, string, string, string, string, api.ObjectVersionLegalHoldRequest) (api.ObjectVersionProtection, error)
	GetObjectVersionProtection(context.Context, string, string, string) (api.ObjectVersionProtection, error)
}

func cmdVersionProtection(args []string) int {
	if err := validateProtectionCLI(args); err != nil {
		PrintUsage(osStderr, "usage: gregale bucket protection <status app bucket operation-id | retention app bucket key version-id [clear operation-id | GOVERNANCE|COMPLIANCE retain-until operation-id] | event-hold app bucket key version-id GOVERNANCE|COMPLIANCE ON days|years N|OFF [--retain-until date] operation-id | legal-hold app bucket key version-id [ON|OFF operation-id]>", "bucket")
		return 1
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := runVersionProtection(context.Background(), c, args)
	if err != nil {
		return printErr("Could not manage version protection", err)
	}
	return jsonOut(writeJSON(out))
}
func validateProtectionCLI(args []string) error {
	if len(args) < 4 || !api.ValidAppSlug(args[1]) {
		return fmt.Errorf("invalid protection arguments")
	}
	if _, err := uuid.Parse(args[2]); err != nil {
		return err
	}
	if args[0] == "status" && len(args) == 4 {
		if !state.ValidObjectVersionID(args[3]) || args[3] == "null" {
			return fmt.Errorf("invalid operation ID")
		}
		return nil
	}
	if len(args) < 5 || !state.ValidObjectVersionID(args[4]) {
		return fmt.Errorf("an exact version is required")
	}
	switch args[0] {
	case "event-hold":
		_, err := eventHoldCLIRequest(args)
		return err
	case "retention":
		if len(args) == 5 {
			return nil
		}
		if len(args) == 7 && args[5] == "clear" {
			if !state.ValidObjectVersionID(args[6]) || args[6] == "null" {
				return fmt.Errorf("invalid operation ID")
			}
			return nil
		}
		if len(args) == 8 && api.ValidObjectLockMode(args[5]) {
			_, err := time.Parse(time.RFC3339Nano, args[6])
			if err != nil {
				return err
			}
			if !state.ValidObjectVersionID(args[7]) || args[7] == "null" {
				return fmt.Errorf("invalid operation ID")
			}
			return nil
		}
	case "legal-hold":
		if len(args) == 5 {
			return nil
		}
		if len(args) == 7 && (args[5] == "ON" || args[5] == "OFF") && state.ValidObjectVersionID(args[6]) && args[6] != "null" {
			return nil
		}
	}
	return fmt.Errorf("invalid protection selection")
}
func runVersionProtection(ctx context.Context, c versionProtectionClient, args []string) (any, error) {
	if err := validateProtectionCLI(args); err != nil {
		return nil, err
	}
	if args[0] == "status" {
		return c.GetObjectVersionProtection(ctx, args[1], args[2], args[3])
	}
	if args[0] == "event-hold" {
		in, err := eventHoldCLIRequest(args)
		if err != nil {
			return nil, err
		}
		return c.PutObjectVersionRetention(ctx, args[1], args[2], args[3], args[4], in)
	}
	if args[0] == "legal-hold" {
		if len(args) == 5 {
			return c.GetObjectVersionLegalHold(ctx, args[1], args[2], args[3], args[4])
		}
		return c.PutObjectVersionLegalHold(ctx, args[1], args[2], args[3], args[4], api.ObjectVersionLegalHoldRequest{ID: args[6], LegalHold: api.ObjectVersionLegalHold{Status: args[5]}})
	}
	if len(args) == 5 {
		return c.GetObjectVersionRetention(ctx, args[1], args[2], args[3], args[4])
	}
	in := api.ObjectVersionRetentionRequest{ID: args[len(args)-1]}
	if args[5] != "clear" {
		d, err := time.Parse(time.RFC3339Nano, args[6])
		if err != nil {
			return nil, err
		}
		in.Retention = api.ObjectVersionRetention{Mode: args[5], RetainUntilDate: &d}
	}
	return c.PutObjectVersionRetention(ctx, args[1], args[2], args[3], args[4], in)
}

func eventHoldCLIRequest(args []string) (api.ObjectVersionRetentionRequest, error) {
	var in api.ObjectVersionRetentionRequest
	if len(args) < 8 || !api.ValidObjectLockMode(args[5]) {
		return in, fmt.Errorf("an event hold requires a mode, status and operation ID")
	}
	in.Retention.Mode, in.Retention.EventHold = args[5], args[6]
	pos := 7
	if args[6] == "ON" {
		if len(args) < 10 || args[7] != "days" && args[7] != "years" {
			return in, fmt.Errorf("an ON hold requires a duration in days or years")
		}
		n, err := strconv.ParseInt(args[8], 10, 32)
		if err != nil {
			return in, err
		}
		value := int32(n)
		in.Retention.EventHoldDuration = &api.ObjectRetentionPeriod{}
		if args[7] == "days" {
			in.Retention.EventHoldDuration.Days = &value
		} else {
			in.Retention.EventHoldDuration.Years = &value
		}
		pos = 9
	}
	if len(args) == pos+3 && args[pos] == "--retain-until" {
		d, err := time.Parse(time.RFC3339Nano, args[pos+1])
		if err != nil {
			return in, err
		}
		in.Retention.RetainUntilDate = &d
		pos += 2
	}
	if len(args) != pos+1 || !state.ValidObjectVersionID(args[pos]) || args[pos] == "null" || !in.Retention.ValidForWrite() {
		return in, fmt.Errorf("invalid event hold policy or operation ID")
	}
	in.ID = args[pos]
	return in, nil
}
