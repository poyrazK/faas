// commands_ai_evaluations.go — `gregale ai evaluate` (ADR-1431): judge a
// route target against the route's first target on recent captured calls,
// optionally starting a canary when it passes.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const aiEvaluateUsage = "usage: gregale ai evaluate <route-id> --position <2-5> --judge <integration-id> --judge-model <model> " +
	"[--sample <1-50>] [--max-loss-rate <0-100>] [--canary-percent <1-50>] [--no-wait] [--timeout 30m] | " +
	"gregale ai evaluate list <route-id> | gregale ai evaluate show <evaluation-id>"

// cmdAIEvaluate records an evaluation and, unless --no-wait, waits for it.
func cmdAIEvaluate(args []string) int {
	if len(args) > 0 && (args[0] == "list" || args[0] == "show") {
		return cmdAIEvaluateRead(args[0], args[1:])
	}
	fs := newFlagSet("ai evaluate", flag.ContinueOnError)
	position := fs.Int("position", 0, "the route target (2 to 5) to evaluate")
	judge := fs.String("judge", "", "the AI integration whose model judges the answers")
	judgeModel := fs.String("judge-model", "", "the judge model")
	sample := fs.Int("sample", api.OutboundAIEvaluationSampleDefault, "captured calls to replay, 1 to 50")
	maxLossRate := fs.Int("max-loss-rate", api.OutboundAIEvaluationMaxLossRateDefault, "pass when losses and errors are at most this percent of the sample")
	canaryPercent := fs.Int("canary-percent", 0, "start a canary with this share of calls when the evaluation passes (0: none)")
	noWait := fs.Bool("no-wait", false, "print the evaluation without waiting for its verdict")
	timeout := fs.Duration("timeout", 30*time.Minute, "how long to wait for the verdict")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 {
		return printErr("Invalid ai evaluate arguments", errors.New(aiEvaluateUsage))
	}
	req := api.CreateOutboundAIEvaluationRequest{Target: *position, JudgeIntegrationID: *judge, JudgeModel: *judgeModel,
		SampleSize: sample, MaxLossRate: maxLossRate}
	if *canaryPercent > 0 {
		req.Canary = &api.PutOutboundAIRouteCanaryRequest{Target: *position, Percent: *canaryPercent}
	}
	if !req.Valid() {
		return printErr("Invalid evaluation", errors.New(aiEvaluateUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	evaluation, err := client.CreateOutboundAIEvaluation(context.Background(), fs.Arg(0), req)
	if err != nil {
		return printErr("Could not start the evaluation", err)
	}
	if !*noWait {
		deadline := time.Now().Add(*timeout)
		for evaluation.State == api.OutboundAIEvaluationPending || evaluation.State == api.OutboundAIEvaluationRunning {
			if time.Now().After(deadline) {
				return printErr("Evaluation still running", fmt.Errorf("check later with gregale ai evaluate show %s", evaluation.ID))
			}
			time.Sleep(2 * time.Second)
			if evaluation, err = client.GetOutboundAIEvaluation(context.Background(), evaluation.ID); err != nil {
				return printErr("Could not read the evaluation", err)
			}
		}
	}
	return printAIEvaluation(evaluation)
}

func cmdAIEvaluateRead(action string, args []string) int {
	fs := newFlagSet("ai evaluate "+action, flag.ContinueOnError)
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 {
		return printErr("Invalid ai evaluate arguments", errors.New(aiEvaluateUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if action == "show" {
		evaluation, err := client.GetOutboundAIEvaluation(context.Background(), fs.Arg(0))
		if err != nil {
			return printErr("Could not read the evaluation", err)
		}
		return printAIEvaluation(evaluation)
	}
	list, err := client.ListOutboundAIEvaluations(context.Background(), fs.Arg(0))
	if err != nil {
		return printErr("Could not list evaluations", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(list))
	}
	if len(list.Items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no evaluations)")
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tTARGET\tSTATE\tVERDICT\tWINS\tTIES\tLOSSES\tERRORS\tCREATED")
	for _, e := range list.Items {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%d\t%d\t%d\t%d\t%s\n", e.ID, e.Target, e.State, aiEvaluationOutcome(e),
			e.Wins, e.Ties, e.Losses, e.CandidateErrors, e.CreatedAt.UTC().Format(time.RFC3339))
	}
	_ = tw.Flush()
	return 0
}

// aiEvaluationOutcome is a verdict, an error code, or "-".
func aiEvaluationOutcome(e api.OutboundAIEvaluation) string {
	switch {
	case e.Verdict != nil:
		return *e.Verdict
	case e.ErrorCode != nil:
		return *e.ErrorCode
	}
	return "-"
}

// printAIEvaluation shows the verdict, both sides' cost and latency, and
// each sample.
func printAIEvaluation(e api.OutboundAIEvaluation) int {
	if jsonOutput {
		return jsonOut(writeJSON(e))
	}
	_, _ = fmt.Fprintf(osStdout, "Evaluation %s of route %s target %d: %s, %s\n", e.ID, e.RouteID, e.Target, e.State, aiEvaluationOutcome(e))
	_, _ = fmt.Fprintf(osStdout, "  %d samples: %d wins, %d ties, %d losses, %d candidate errors, %d judge errors (pass at %d%% losses and errors)\n",
		e.Samples, e.Wins, e.Ties, e.Losses, e.CandidateErrors, e.JudgeErrors, e.MaxLossRate)
	_, _ = fmt.Fprintf(osStdout, "  baseline: cost %s, mean TTFB %s; candidate: cost %s, mean TTFB %s\n",
		financialMoney(e.Baseline.CostMillicents), formatOptionalCount(e.Baseline.MeanTTFBMS),
		financialMoney(e.Candidate.CostMillicents), formatOptionalCount(e.Candidate.MeanTTFBMS))
	if e.Canary != nil {
		started := "not started"
		if e.CanaryStarted {
			started = "started"
		}
		_, _ = fmt.Fprintf(osStdout, "  canary at %d%%: %s\n", e.Canary.Percent, started)
	}
	if len(e.Items) == 0 {
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "CALL\tVERDICT\tSTATUS\tCOST\tTTFB\tBASELINE\tCANDIDATE")
	for _, item := range e.Items {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", item.LogID, item.Verdict, formatOptionalInt(item.CandidateStatus),
			financialMoney(item.CandidateCostMillicents), formatOptionalCount(item.CandidateTTFBMS),
			replayExcerpt(item.BaselineOutputText), replayExcerpt(item.CandidateOutputText))
	}
	_ = tw.Flush()
	return 0
}

func formatOptionalInt(value *int) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprint(*value)
}
