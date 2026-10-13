package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdCommitDeliveryReport(args []string) int {
	fs := newFlagSet("commit delivery-report", flag.ContinueOnError)
	refresh := fs.String("refresh", "", "reread a saved selection; mutually exclusive with resume")
	resume := fs.String("resume", "", "saved report JSON file; fixes publication selection and subscriber filter")
	pages := fs.Int("recipient-pages", 1, "recipient pages per publication (1..20)")
	before := fs.String("before", "", "history next_before cursor")
	subscription := fs.String("subscription-id", "", "filter original application subscriber")
	limit := fs.Int("limit", api.CommitPublicationHistoryPageDefault, "publications per page")
	timeout := fs.Duration("timeout", 2*time.Minute, "overall read timeout")
	flags, positional := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil || rejectUnexpectedFlagArgs(fs) || len(positional) != 1 || *limit < 1 || *limit > api.CommitPublicationHistoryPageMax || *pages < 1 || *pages > api.CommitDeliveryReportPagesMax || *timeout <= 0 || *timeout > 24*time.Hour {
		return 1
	}
	for _, id := range []string{positional[0], *subscription} {
		if id == "" {
			continue
		}
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil {
			return printErr("Invalid Commit identity", api.ErrValidation("identity must be a nonzero UUID"))
		}
	}
	if *resume != "" && *refresh != "" {
		return printErr("Choose resume or refresh", fmt.Errorf("saved report modes are mutually exclusive"))
	}
	if *resume != "" || *refresh != "" {
		invalid := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "before" || f.Name == "limit" || f.Name == "subscription-id" {
				invalid = true
			}
		})
		if invalid {
			return printErr("Saved report fixes selection", fmt.Errorf("omit before, limit and subscription-id"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	var out api.CommitDeliveryReport
	if *resume != "" || *refresh != "" {
		path := *resume
		if *refresh != "" {
			path = *refresh
		}
		saved, readErr := readCommitDeliveryReport(path)
		if readErr != nil {
			return printErr("Cannot read saved report", readErr)
		}
		if saved.SourceID != positional[0] {
			return printErr("Saved report source mismatch", fmt.Errorf("use the original source UUID"))
		}
		if *refresh != "" {
			out, err = client.RefreshCommitDeliveryReport(ctx, saved, *pages)
		} else {
			out, err = client.ResumeCommitDeliveryReport(ctx, saved, *pages)
		}
	} else {
		out, err = client.GetCommitDeliveryReportWithPages(ctx, positional[0], *before, *subscription, *limit, *pages)
	}
	if code := jsonOut(writeJSON(out)); code != 0 {
		return code
	}
	if err != nil {
		return commitSubscriberWaitError(err)
	}
	return 0
}

func readCommitDeliveryReport(path string) (api.CommitDeliveryReport, error) {
	var out api.CommitDeliveryReport
	f, err := openCustomerFile(path)
	if err != nil {
		return out, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, api.CommitDeliveryReportFileMaxBytes+1))
	if err != nil {
		return out, err
	}
	if len(raw) > api.CommitDeliveryReportFileMaxBytes {
		return out, fmt.Errorf("saved report exceeds size limit")
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}
