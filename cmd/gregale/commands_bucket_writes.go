package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type bucketWritesOptions struct {
	action, app, bucket, id, status, cursor string
	limit                                   int
	timeout, interval                       time.Duration
}

func cmdBucketWrites(args []string) int {
	o, err := parseBucketWrites(args)
	if err != nil {
		PrintUsage(osStderr, "usage: gregale bucket writes list <app> <bucket-id> [--status=pending|completed|failed|all] [--limit=N] [--cursor=TOKEN] | gregale bucket writes <status|wait> <app> <bucket-id> <receipt-id> [--timeout=5m] [--poll-interval=5s]", "bucket")
		return 1
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if o.action == "list" {
		return listBucketWrites(ctx, c, o)
	}
	if o.action == "wait" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.timeout)
		defer cancel()
	}
	receipt, err := readBucketWrite(ctx, c, o)
	if receipt.ID != "" {
		if code := printBucketWrite(receipt); code != 0 {
			return code
		}
	}
	if err != nil {
		return printErr("Could not read write receipt", err)
	}
	if o.action == "wait" && receipt.Status == "failed" {
		return 1
	}
	return 0
}

func parseBucketWrites(args []string) (bucketWritesOptions, error) {
	o := bucketWritesOptions{}
	if len(args) < 3 || !api.ValidAppSlug(args[1]) {
		return o, errors.New("invalid bucket writes command")
	}
	o.action, o.app, o.bucket = args[0], args[1], args[2]
	if _, err := uuid.Parse(o.bucket); err != nil {
		return o, err
	}
	fs := flag.NewFlagSet("bucket writes", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	flags := args[3:]
	switch o.action {
	case "list":
		fs.StringVar(&o.status, "status", "pending", "receipt status")
		fs.IntVar(&o.limit, "limit", api.ObjectWriteReceiptPageDefault, "page size")
		fs.StringVar(&o.cursor, "cursor", "", "next page cursor")
	case "status", "wait":
		if len(flags) == 0 {
			return o, errors.New("receipt ID is required")
		}
		o.id, flags = flags[0], flags[1:]
		if _, err := uuid.Parse(o.id); err != nil {
			return o, err
		}
		if o.action == "wait" {
			fs.DurationVar(&o.timeout, "timeout", api.ObjectWriteReceiptWaitTimeout, "maximum wait")
			fs.DurationVar(&o.interval, "poll-interval", api.ObjectWriteReceiptPollInterval, "time between reads")
		}
	default:
		return o, errors.New("invalid write action")
	}
	if err := fs.Parse(flags); err != nil {
		return o, err
	}
	return validateBucketWrites(o, fs.NArg())
}

func validateBucketWrites(o bucketWritesOptions, extra int) (bucketWritesOptions, error) {
	if extra != 0 || o.action == "wait" && (o.timeout <= 0 || o.interval < api.ObjectWriteReceiptMinPollInterval) {
		return o, errors.New("invalid wait or trailing arguments")
	}
	if o.action == "list" {
		status, limit, ok := api.ParseObjectWriteReceiptPage(o.status, o.limit, o.cursor)
		if !ok || o.limit < 1 {
			return o, errors.New("invalid list options")
		}
		o.status, o.limit = status, limit
	}
	return o, nil
}

type bucketWritesClient interface {
	GetObjectWriteReceipt(context.Context, string, string, string) (api.ObjectWriteReceipt, error)
	ListObjectWriteReceipts(context.Context, string, string, string, int, string) (api.ObjectWriteReceiptList, error)
}

func readBucketWrite(ctx context.Context, c bucketWritesClient, o bucketWritesOptions) (api.ObjectWriteReceipt, error) {
	var last api.ObjectWriteReceipt
	for {
		if err := ctx.Err(); err != nil {
			return last, fmt.Errorf("receipt wait ended: %w", err)
		}
		r, err := c.GetObjectWriteReceipt(ctx, o.app, o.bucket, o.id)
		if err != nil {
			return last, err
		}
		last = r
		if o.action != "wait" || r.Status == "completed" || r.Status == "failed" {
			return r, nil
		}
		if r.Status != "pending" {
			return last, errors.New("invalid receipt status")
		}
		timer := time.NewTimer(o.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, fmt.Errorf("receipt still pending: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func printBucketWrite(r api.ObjectWriteReceipt) int {
	if jsonOutput {
		return jsonOut(writeJSON(r))
	}
	_, _ = fmt.Fprintf(osStdout, "Write %s: %s (%s, %d bytes)\nKey: %q\n", r.ID, r.Status, r.Operation, r.Bytes, r.Key)
	if r.ErrorCode != "" {
		_, _ = fmt.Fprintf(osStdout, "Reason: %s\n", r.ErrorCode)
	}
	return 0
}

func listBucketWrites(ctx context.Context, c bucketWritesClient, o bucketWritesOptions) int {
	page, err := c.ListObjectWriteReceipts(ctx, o.app, o.bucket, o.status, o.limit, o.cursor)
	if err != nil {
		return printErr("Could not list write receipts", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	for _, receipt := range page.Items {
		_ = printBucketWrite(receipt)
	}
	if page.NextCursor != "" {
		_, _ = fmt.Fprintf(osStdout, "Next cursor: %q\n", page.NextCursor)
	}
	return 0
}
