package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdJobsAddInteractive() int {
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use jobs add NAME --image REF for scripts"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	req := api.CreateJobRequest{Kind: "batch"}
	for {
		req.Name, err = prompt.text(ctx, "Job name (3–40 lowercase letters, digits, hyphens)", "")
		if err != nil {
			return startInputExit(err)
		}
		if jobSlugPattern.MatchString(req.Name) {
			break
		}
		_, _ = fmt.Fprintln(osStderr, "Enter a valid Job name.")
	}
	for {
		req.ImageRef, err = prompt.text(ctx, "Container image (name:tag or name@digest)", "")
		if err != nil {
			return startInputExit(err)
		}
		if strings.TrimSpace(req.ImageRef) != "" && !strings.ContainsAny(req.ImageRef, " \t\r\n\x00") {
			break
		}
		_, _ = fmt.Fprintln(osStderr, "Enter a nonempty image reference without whitespace.")
	}
	custom, err := prompt.confirm(ctx, "Override the image entrypoint with a custom command?")
	if err != nil {
		return startInputExit(err)
	}
	if custom {
		for i := 0; i < 64; i++ {
			label := fmt.Sprintf("Command argument %d (literal value, no shell parsing)", i+1)
			if i == 0 {
				label = "Executable (literal value, no shell parsing)"
			}
			value, inputErr := prompt.text(ctx, label, "")
			if inputErr != nil {
				return startInputExit(inputErr)
			}
			if value == "" || strings.ContainsRune(value, 0) {
				_, _ = fmt.Fprintln(osStderr, "Enter a nonempty argument without NUL characters.")
				i--
				continue
			}
			req.Command = append(req.Command, value)
			if i == 63 {
				break
			}
			more, inputErr := prompt.confirm(ctx, "Add another command argument?")
			if inputErr != nil {
				return startInputExit(inputErr)
			}
			if !more {
				break
			}
		}
	}
	kind, inputErr := prompt.choose(ctx, "Job execution", []string{"Batch (run manually)", "Recurring (run on a schedule)"}, 0)
	if inputErr != nil {
		return startInputExit(inputErr)
	}
	if kind == 1 {
		req.Kind = "recurring"
		req.Schedule, req.Timezone, err = promptJobSchedule(ctx, prompt)
		if err != nil {
			return startInputExit(err)
		}
	}
	resourceMode, err := prompt.choose(ctx, "Resource settings", []string{"Use server defaults", "Choose custom settings"}, 0)
	if err != nil {
		return startInputExit(err)
	}
	if resourceMode == 1 {
		maxLimit := func(limits []int) int {
			n := 0
			for _, v := range limits {
				if v > n {
					n = v
				}
			}
			return n
		}
		req.RAMMB, err = scalePromptInt(ctx, prompt, "RAM per task (MB)", api.JobDefaultRAMMB, 1, maxLimit(api.JobRAMMB[:]))
		if err != nil {
			return startInputExit(err)
		}
		req.TaskTimeoutSec, err = scalePromptInt(ctx, prompt, "Task timeout (seconds)", api.JobDefaultTaskTimeoutSec, 1, maxLimit(api.JobTaskTimeoutSec[:]))
		if err != nil {
			return startInputExit(err)
		}
		req.MaxParallelism, err = scalePromptInt(ctx, prompt, "Maximum parallelism", api.JobDefaultParallelism, 1, maxLimit(api.JobMaxParallelismPerRun[:]))
		if err != nil {
			return startInputExit(err)
		}
		req.RetryMax, err = scalePromptInt(ctx, prompt, "Retry maximum", api.JobDefaultRetryMax, 0, maxLimit(api.JobMaxRetries[:]))
		if err != nil {
			return startInputExit(err)
		}
	}
	PrintProgress(osStdout, "Create %s Job: %s\nImage: %s", req.Kind, req.Name, oneLine(req.ImageRef))
	if len(req.Command) == 0 {
		PrintProgress(osStdout, "Command: image entrypoint")
	} else {
		command, _ := json.Marshal(req.Command)
		PrintProgress(osStdout, "Command arguments: %s", command)
	}
	if resourceMode == 0 {
		PrintProgress(osStdout, "Resources: server defaults (RAM %d MB, timeout %d seconds, parallelism %d, retry maximum %d)", api.JobDefaultRAMMB, api.JobDefaultTaskTimeoutSec, api.JobDefaultParallelism, api.JobDefaultRetryMax)
	} else {
		PrintProgress(osStdout, "Resources: RAM %d MB, timeout %d seconds, parallelism %d, retry maximum %d", req.RAMMB, req.TaskTimeoutSec, req.MaxParallelism, req.RetryMax)
	}
	if req.Kind == "recurring" {
		if err := previewJobSchedule(req.Schedule, req.Timezone); err != nil {
			return printErr("Could not preview schedule", err)
		}
		PrintProgress(osStdout, "This creates an active recurring Job. Scheduled runs can start automatically once its image is ready. Default scheduling policy applies; execution may be delayed or skipped.")
	} else {
		PrintProgress(osStdout, "Creation prepares the image; it does not start a run.")
	}
	PrintProgress(osStdout, "Account plan limits are checked by the server.")
	confirmed, err := prompt.confirm(ctx, "Create this Job?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Job creation canceled.")
		return 0
	}
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	job, err := client.CreateJob(writeCtx, req)
	if err != nil {
		return printErr("Create failed", err)
	}
	if job.Name != req.Name || !jobRunIDPattern.MatchString(job.ID) {
		return printErr("Invalid creation receipt", errors.New("server returned another Job; inspect Jobs before creating again"))
	}
	PrintOK(osStdout, "Job %s created (image=%s RAM=%dMB timeout=%ds image-status=%s).", job.Name, oneLine(job.ImageRef), job.RAMMB, job.TaskTimeoutSec, oneLine(job.ImageMaterializationStatus))
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	_, _ = fmt.Fprintln(osStdout, "Inspection command (POSIX shells):\n"+strings.Join(append(command, "jobs", "info", quoteLogCommandArg(job.Name)), " "))
	if job.ImageMaterializationStatus == "pending" || job.ImageMaterializationStatus == "verifying_legacy" {
		follow, inputErr := prompt.confirm(ctx, "Wait up to 5 minutes for this image to become ready?")
		if inputErr != nil {
			return startInputExit(inputErr)
		}
		if follow {
			return outputJobImageWait(ctx, client, job, 5*time.Minute, 2*time.Second)
		}
	}
	return 0
}
