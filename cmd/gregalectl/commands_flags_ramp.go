package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/platformflags"
)

// rampOptions are the parsed `flags ramp` flags.
type rampOptions struct {
	stages       []int
	interval     time.Duration
	maxFailureBP int
	minRequests  int64
	onBreach     string
	resume       bool
	remove       bool
}

// cmdFlagsRamp makes the rollout rule advance itself stage by stage (ADR
// 20261021080640), resumes a paused ramp, or removes the ramp.
func cmdFlagsRamp(args []string) int {
	fs := flagsFlagSet("ramp")
	key, mutation := flagsMutationFlags(fs)
	stages := fs.String("stages", "", "comma-separated rollout percentages, ascending, including the current one (e.g. 1,10,50,100)")
	interval := fs.Duration("interval", 30*time.Minute, "how long each stage must hold before it is judged (>= 5m)")
	maxFailures := fs.Float64("max-5xx-percent", 1, "highest 5xx share of the flag's health routes that still advances")
	minRequests := fs.Int64("min-requests", 100, "requests a stage needs before it is judged")
	onBreach := fs.String("on-breach", platformflags.RampPause, "pause, or rollback to the previous stage and pause")
	resume := fs.Bool("resume", false, "clear a paused ramp so it continues")
	remove := fs.Bool("remove", false, "remove the ramp and keep the current rollout")
	if err := fs.Parse(args); err != nil || !flagsNoPositional(fs, "ramp") {
		return 2
	}
	opts, err := parseRampOptions(*stages, *interval, *maxFailures, *minRequests, *onBreach, *resume, *remove)
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl flags ramp: %v\n", err)
		return 2
	}
	mutation.name = "ramp"
	mutation.edit = func(def *platformflags.Definition) error { return applyRamp(def, opts) }
	return runFlagsMutation(*key, mutation)
}

func parseRampOptions(stages string, interval time.Duration, maxFailures float64, minRequests int64, onBreach string, resume, remove bool) (rampOptions, error) {
	opts := rampOptions{interval: interval, minRequests: minRequests, onBreach: strings.TrimSpace(onBreach), resume: resume, remove: remove}
	if resume && remove {
		return opts, fmt.Errorf("--resume and --remove are exclusive")
	}
	if resume || remove {
		return opts, nil
	}
	for _, raw := range strings.Split(stages, ",") {
		if raw = strings.TrimSpace(raw); raw == "" {
			continue
		}
		percent, err := strconv.ParseFloat(raw, 64)
		if err != nil || percent <= 0 || percent > 100 {
			return opts, fmt.Errorf("stage %q must be a percentage in (0, 100]", raw)
		}
		opts.stages = append(opts.stages, int(percent*100+0.5))
	}
	if len(opts.stages) < 2 {
		return opts, fmt.Errorf("--stages needs at least two percentages")
	}
	if maxFailures < 0 || maxFailures > 100 {
		return opts, fmt.Errorf("--max-5xx-percent must be between 0 and 100")
	}
	opts.maxFailureBP = int(maxFailures*100 + 0.5)
	return opts, nil
}

// applyRamp edits the rollout rule; flags rollout creates it.
func applyRamp(def *platformflags.Definition, opts rampOptions) error {
	at := slices.IndexFunc(def.Rules, func(rule platformflags.Rule) bool { return rule.ID == flagsRolloutRuleID })
	if at < 0 || def.Rules[at].Rollout == nil {
		return fmt.Errorf("the flag has no rollout rule; create one with flags rollout --percent")
	}
	rule := &def.Rules[at]
	switch {
	case opts.remove:
		rule.Ramp = nil
		return nil
	case opts.resume:
		if rule.Ramp == nil {
			return fmt.Errorf("the rollout rule has no ramp to resume")
		}
		rule.Ramp.Paused, rule.Ramp.PausedReason = false, ""
		return nil
	}
	if !slices.Contains(opts.stages, *rule.Rollout) {
		return fmt.Errorf("the rollout is at %s%%; include it in --stages so the ramp starts there", strconv.FormatFloat(float64(*rule.Rollout)/100, 'f', -1, 64))
	}
	rule.Ramp = &platformflags.Ramp{
		Stages: opts.stages, IntervalSeconds: int(opts.interval / time.Second), Max5xxBasisPoints: opts.maxFailureBP,
		MinRequests: opts.minRequests, OnBreach: opts.onBreach,
	}
	return nil
}
