package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type customerOperationDeliveryClient interface {
	BaseURL() string
	Whoami(context.Context) (api.AccountResponse, error)
	GetOperationDelivery(context.Context, string, string) (api.OperationDeliveryInspection, error)
	GetOperationDeliveryAttempts(context.Context, string, string, int, string) (api.OperationDeliveryAttemptsResponse, error)
	RetryOperationDeliveryWithReceipt(context.Context, string, string, api.OperationDeliveryRetryRequest) (api.OperationDeliveryRetryResponse, error)
}
type customerOperationDeliveryCommand struct {
	verb, app, id, cursor, receipt string
	limit                          int
	timeout                        time.Duration
	request                        api.OperationDeliveryRetryRequest
}

func parseCustomerOperationDelivery(args []string) (customerOperationDeliveryCommand, error) {
	var c customerOperationDeliveryCommand
	if len(args) == 0 {
		return c, fmt.Errorf("missing delivery command")
	}
	c.verb = args[0]
	fs := newFlagSet("customer-operations "+c.verb, flag.ContinueOnError)
	fs.StringVar(&c.app, "app", "", "account-owned application")
	fs.DurationVar(&c.timeout, "timeout", 0, "local request deadline")
	generation := 0
	switch c.verb {
	case "delivery":
	case "delivery-attempts":
		fs.IntVar(&c.limit, "limit", api.OperationHistoryPageDefault, "bounded page size")
		fs.StringVar(&c.cursor, "cursor", "", "opaque next cursor")
	case "retry-delivery":
		fs.StringVar(&c.request.DeliveryID, "delivery", "", "observed delivery ID")
		fs.StringVar(&c.request.RetryID, "retry-id", "", "stable retry decision ID")
		fs.IntVar(&generation, "expected-replay-generation", 0, "observed dead notification generation")
		fs.StringVar(&c.receipt, "receipt-file", "", "private immutable request receipt")
	default:
		return c, fmt.Errorf("unknown delivery command")
	}
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return c, err
	}
	if fs.NArg() != 1 || c.app == "" || !validCustomerOperationUUID(fs.Arg(0)) || c.timeout < 0 {
		return c, fmt.Errorf("delivery commands require an operation UUID and --app")
	}
	c.id = fs.Arg(0)
	if c.verb == "delivery-attempts" && (c.limit < 1 || c.limit > api.OperationHistoryPageMax || len(c.cursor) > api.OperationHistoryCursorMaxBytes) {
		return c, fmt.Errorf("invalid attempt page")
	}
	if c.verb == "retry-delivery" {
		provided := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "expected-replay-generation" {
				provided = true
			}
		})
		if provided {
			c.request.ExpectedReplayGeneration = &generation
		}
		if c.receipt == "" {
			return c, fmt.Errorf("retry-delivery requires --receipt-file; it records the request before mutation")
		}
		if c.request.RetryID != "" || c.request.DeliveryID != "" || provided {
			if !validCustomerOperationKey(c.request.RetryID) || !validCustomerOperationUUID(c.request.DeliveryID) || !provided || generation < 0 || generation >= math.MaxInt32 {
				return c, fmt.Errorf("new retry requires --delivery, --retry-id and --expected-replay-generation; omit all three to resume")
			}
		}
	}
	return c, nil
}

func cmdCustomerOperationDelivery(args []string) int {
	c, err := parseCustomerOperationDelivery(args)
	if err != nil {
		return printErr("Invalid delivery command", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	err = runCustomerOperationDelivery(ctx, client, c, osStdout, jsonOutput, time.Now().UTC())
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return 130
	}
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
		_ = printErr("Local delivery request timed out; resume using its receipt", err)
		return 124
	}
	if err != nil {
		return printErr("Delivery command failed", err)
	}
	return 0
}

func runCustomerOperationDelivery(ctx context.Context, client customerOperationDeliveryClient, c customerOperationDeliveryCommand, out io.Writer, asJSON bool, now time.Time) error {
	switch c.verb {
	case "retry-delivery":
		r, err := retryCustomerOperationDelivery(ctx, client, c, now)
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(out).Encode(r)
		}
		_, err = fmt.Fprintf(out, "Retry decision %s: queued generation %d at %s\nThis is the recorded decision; inspect delivery for its current state.\n", r.RetryID, r.ReplayGeneration, r.QueuedAt.UTC().Format(time.RFC3339Nano))
		return err
	case "delivery":
		r, err := client.GetOperationDelivery(ctx, c.app, c.id)
		if err != nil {
			return err
		}
		if !sameCustomerOperationUUID(r.OperationID, c.id) || r.ObservedAt.IsZero() {
			return fmt.Errorf("mismatched delivery report")
		}
		if asJSON {
			return json.NewEncoder(out).Encode(r)
		}
		_, err = fmt.Fprintf(out, "Business: %s\nDelivery: %s; attempts %d; HTTP %d\n", r.BusinessState, r.State, r.Attempts, r.LastResponseCode)
		if err == nil && r.ReplayGeneration != nil {
			_, err = fmt.Fprintf(out, "Delivery ID: %s; replay generation %d\n", r.DeliveryID, *r.ReplayGeneration)
		}
		if err == nil && r.ErrorCode != "" {
			_, err = fmt.Fprintf(out, "Reason: %s\n", r.ErrorCode)
		}
		if err == nil && r.NextAttemptAt != nil {
			_, err = fmt.Fprintf(out, "Next attempt: %s\n", r.NextAttemptAt.UTC().Format(time.RFC3339Nano))
		}
		if err == nil && r.ReceiverState != "" {
			_, err = fmt.Fprintf(out, "Receiver: %s\n", r.ReceiverState)
		}
		if err == nil && r.ReceiverCooldownUntil != nil {
			_, err = fmt.Fprintf(out, "Receiver cooldown until: %s\n", r.ReceiverCooldownUntil.UTC().Format(time.RFC3339Nano))
		}
		return err
	case "delivery-attempts":
		r, err := client.GetOperationDeliveryAttempts(ctx, c.app, c.id, c.limit, c.cursor)
		if err != nil {
			return err
		}
		if !sameCustomerOperationUUID(r.OperationID, c.id) || !validCustomerOperationUUID(r.DeliveryID) || len(r.Attempts) > c.limit || len(r.NextCursor) > api.OperationHistoryCursorMaxBytes {
			return fmt.Errorf("mismatched delivery attempt page")
		}
		if asJSON {
			return json.NewEncoder(out).Encode(r)
		}
		for _, a := range r.Attempts {
			if _, err = fmt.Fprintf(out, "%d/%d\t%s\tHTTP %d\t%s\t%s\n", a.ReplayGeneration, a.AttemptNumber, a.Outcome, a.ResponseCode, a.ErrorCode, a.FinishedAt.UTC().Format(time.RFC3339Nano)); err != nil {
				return err
			}
		}
		if r.NextCursor != "" {
			_, err = fmt.Fprintf(out, "Next cursor: %s\n", r.NextCursor)
		}
		return err
	}
	return fmt.Errorf("unknown delivery command")
}
