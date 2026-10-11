package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdJobsInfoWaitArgs(args []string) int {
	fs := newFlagSet("jobs info", flag.ContinueOnError)
	wait := fs.Bool("wait-ready", false, "wait for image preparation")
	timeout := fs.Duration("timeout", 5*time.Minute, "maximum wait duration")
	interval := fs.Duration("poll-interval", 2*time.Second, "polling interval")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if !*wait || fs.NArg() != 1 || !jobSlugPattern.MatchString(fs.Arg(0)) || *timeout <= 0 || *interval <= 0 {
		return printErr("Invalid image wait arguments", errors.New("use jobs info NAME --wait-ready with positive --timeout and --poll-interval"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	job, err := client.GetJob(readCtx, fs.Arg(0))
	cancel()
	if err != nil {
		return printErr("Could not read Job", err)
	}
	if job.Name != fs.Arg(0) || !jobRunIDPattern.MatchString(job.ID) {
		return printErr("Invalid Job", errors.New("server returned another Job"))
	}
	return outputJobImageWait(ctx, client, job, *timeout, *interval)
}

func outputJobImageWait(ctx context.Context, client *api.Client, job api.JobResponse, timeout, interval time.Duration) int {
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "jobs", "info", quoteLogCommandArg(job.Name), "--wait-ready", "--timeout", timeout.String(), "--poll-interval", interval.String())
	resume := strings.Join(command, " ")
	if !jsonOutput {
		_, _ = fmt.Fprintln(osStdout, "Resume command (POSIX shells):\n"+resume)
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	last, code, err := followJobImage(waitCtx, client, job, interval)
	if jsonOutput {
		receipt := struct {
			Job           api.JobResponse `json:"job"`
			ExitCode      int             `json:"exit_code"`
			ResumeCommand string          `json:"resume_command"`
			Error         string          `json:"error,omitempty"`
		}{Job: last, ExitCode: code, ResumeCommand: resume}
		if err != nil {
			receipt.Error = err.Error()
		}
		if outputCode := jsonOut(writeJSONSingle(receipt)); outputCode != 0 {
			return outputCode
		}
	} else {
		renderJobState(osStdout, last)
		if err != nil {
			printErr("Image wait ended", err)
		}
	}
	return code
}

func followJobImage(ctx context.Context, client *api.Client, pin api.JobResponse, interval time.Duration) (api.JobResponse, int, error) {
	last := pin
	previous := ""
	for {
		if last.ID != pin.ID || last.Name != pin.Name || last.AccountID != pin.AccountID || last.ImageRef != pin.ImageRef {
			return last, 1, errors.New("job identity or selected image changed; inspect the Job and start a new wait")
		}
		if pin.ImageResolvedDigest != "" && last.ImageResolvedDigest != pin.ImageResolvedDigest {
			return last, 1, errors.New("resolved image digest changed")
		}
		if pin.ImageResolvedDigest == "" && last.ImageResolvedDigest != "" {
			pin.ImageResolvedDigest = last.ImageResolvedDigest
		}
		progress := fmt.Sprintf("Job %s: image preparation %s", pin.Name, oneLine(last.ImageMaterializationStatus))
		if !jsonOutput && progress != previous {
			PrintProgress(osStdout, "%s", progress)
			previous = progress
		}
		switch last.ImageMaterializationStatus {
		case "ready":
			return last, 0, nil
		case "failed":
			return last, 1, fmt.Errorf("image preparation failed: %s", oneLine(last.ImageMaterializationError))
		case "pending", "verifying_legacy":
		default:
			return last, 1, errors.New("unknown image preparation status")
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if errors.Is(ctx.Err(), context.Canceled) {
				return last, 130, ctx.Err()
			}
			return last, 124, ctx.Err()
		case <-timer.C:
		}
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		got, err := client.GetJob(readCtx, pin.Name)
		cancel()
		if err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return last, 130, ctx.Err()
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return last, 124, ctx.Err()
			}
			return last, 1, err
		}
		last = got
	}
}
