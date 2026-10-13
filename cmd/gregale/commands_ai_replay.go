// commands_ai_replay.go — `gregale ai replay` (ADR-997): resend a captured
// AI call to other integrations or models and compare the results.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const aiReplayUsage = "usage: gregale ai replay <integration-id> <request-id> --target <integration-id>[:<model>]... [--no-wait] [--timeout 3m] | " +
	"gregale ai replay show <replay-id>"

// cmdAIReplay records a replay and, unless --no-wait, waits for its results.
func cmdAIReplay(args []string) int {
	if len(args) > 0 && args[0] == "show" {
		return cmdAIReplayShow(args[1:])
	}
	fs := newFlagSet("ai replay", flag.ContinueOnError)
	var targets stringListFlag
	fs.Var(&targets, "target", "<integration-id>[:<model>] to replay the call on (repeat, at most 5)")
	noWait := fs.Bool("no-wait", false, "print the replay ID without waiting for results")
	timeout := fs.Duration("timeout", 3*time.Minute, "how long to wait for results")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 2 || len(targets) == 0 {
		return printErr("Invalid ai replay arguments", errors.New(aiReplayUsage))
	}
	req := api.CreateOutboundAIReplayRequest{}
	for _, spec := range targets {
		integrationID, model, _ := strings.Cut(spec, ":")
		req.Targets = append(req.Targets, api.OutboundAIReplayTarget{IntegrationID: integrationID, Model: model})
	}
	if !req.Valid() {
		return printErr("Invalid --target", errors.New("1 to 5 targets as <integration-id>[:<model>]"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	replay, err := client.CreateOutboundAIReplay(context.Background(), fs.Arg(0), fs.Arg(1), req)
	if err != nil {
		return printErr("Could not replay the call", err)
	}
	if !*noWait {
		deadline := time.Now().Add(*timeout)
		for replay.State == api.OutboundAIReplayPending || replay.State == api.OutboundAIReplayRunning {
			if time.Now().After(deadline) {
				return printErr("Replay still running", fmt.Errorf("check later with gregale ai replay show %s", replay.ID))
			}
			time.Sleep(time.Second)
			if replay, err = client.GetOutboundAIReplay(context.Background(), replay.ID); err != nil {
				return printErr("Could not read the replay", err)
			}
		}
	}
	return printAIReplay(replay)
}

func cmdAIReplayShow(args []string) int {
	fs := newFlagSet("ai replay show", flag.ContinueOnError)
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 {
		return printErr("Invalid ai replay show arguments", errors.New(aiReplayUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	replay, err := client.GetOutboundAIReplay(context.Background(), fs.Arg(0))
	if err != nil {
		return printErr("Could not read the replay", err)
	}
	return printAIReplay(replay)
}

// printAIReplay compares the captured call with each target.
func printAIReplay(replay api.OutboundAIReplay) int {
	if jsonOutput {
		return jsonOut(writeJSON(replay))
	}
	_, _ = fmt.Fprintf(osStdout, "Replay %s: %s", replay.ID, replay.State)
	if replay.ErrorCode != nil {
		_, _ = fmt.Fprintf(osStdout, " (%s)", *replay.ErrorCode)
	}
	_, _ = fmt.Fprintln(osStdout)
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "CALL\tMODEL\tSTATUS\tIN\tOUT\tCOST\tMS\tTTFB\tOUTPUT")
	source := replay.Source
	_, _ = fmt.Fprintf(tw, "original\t%s\t%d\t%d\t%d\t%s\t%d\t%s\t%s\n", source.Model, source.Status, source.InputTokens,
		source.OutputTokens, financialMoney(source.CostMillicents), source.DurationMS, formatOptionalCount(source.TimeToFirstByteMS),
		replayExcerpt(derefString(source.OutputText)))
	for _, result := range replay.Results {
		status := "-"
		if result.Status != nil {
			status = fmt.Sprint(*result.Status)
		}
		output := replayExcerpt(result.OutputText)
		if result.ErrorCode != nil {
			output = *result.ErrorCode
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\t%d\t%s\t%s\n", result.IntegrationID, derefString(result.Model), status,
			result.InputTokens, result.OutputTokens, financialMoney(result.CostMillicents), result.DurationMS,
			formatOptionalCount(result.TimeToFirstByteMS), output)
	}
	_ = tw.Flush()
	return 0
}

// replayExcerpt shows the first line of an output, at most 60 characters.
func replayExcerpt(text string) string {
	text, _, _ = strings.Cut(strings.TrimSpace(text), "\n")
	if runes := []rune(text); len(runes) > 60 {
		text = string(runes[:57]) + "..."
	}
	if text == "" {
		return "-"
	}
	return text
}

func derefString(value *string) string {
	if value == nil {
		return "-"
	}
	return *value
}
