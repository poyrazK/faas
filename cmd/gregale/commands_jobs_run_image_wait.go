package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func waitForJobRunImage(parent context.Context, client *api.Client, name string, timeout, interval time.Duration) int {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	fail := func(err error, code int) int {
		if errors.Is(ctx.Err(), context.Canceled) {
			code = 130
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = 124
		}
		command := fmt.Sprintf("%s jobs info %s --wait-ready", recoveryCLI(), recoveryArgument(name))
		if jsonOutput {
			receipt := struct {
				Job            string `json:"job"`
				Phase          string `json:"phase"`
				Submitted      bool   `json:"submitted"`
				ExitCode       int    `json:"exit_code"`
				Error          string `json:"error"`
				InspectCommand string `json:"inspect_command"`
			}{name, "image_wait", false, code, err.Error(), command}
			if outputCode := jsonOut(writeJSONSingle(receipt)); outputCode != 0 {
				return outputCode
			}
		} else {
			printErr("Image preparation ended before submission", fmt.Errorf("%w; no run submitted; inspect %s", err, command))
		}
		return code
	}
	readCtx, readCancel := context.WithTimeout(ctx, 30*time.Second)
	job, err := client.GetJob(readCtx, name)
	readCancel()
	if err != nil {
		return fail(err, 1)
	}
	if job.Name != name || !jobRunIDPattern.MatchString(job.ID) || job.AccountID == "" {
		return fail(errors.New("server did not identify the requested Job and account"), 1)
	}
	_, code, err := followJobImage(ctx, client, job, interval)
	if err != nil {
		return fail(err, code)
	}
	if err := ctx.Err(); err != nil {
		return fail(err, 1)
	}
	return 0
}
