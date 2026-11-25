// Package flagnotify composes flag notifications and delivers them
// (ADR-20261125085913). apid queues experiment decisions and problems,
// weekly lifecycle digests and flag expiries into the flag_notifications
// outbox; meterd's Runner sends them to the captured notification channels.
package flagnotify

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Channels are the channels a notification goes to: the experiment's own
// that still exist, else the environment's. own reports which.
func Channels(liveOwn, environment []string) (channels []string, own bool) {
	if len(liveOwn) > 0 {
		return slices.Clone(liveOwn), true
	}
	return slices.Clone(environment), false
}

// Where names an environment in messages and commands.
type Where struct {
	AccountID, EnvironmentID string
	// OrgID is the project's organization, for the release decision.
	OrgID                string
	Project, Environment string
}

func (w Where) flags() string {
	return fmt.Sprintf("--project %s --environment %s", w.Project, w.Environment)
}

// writeSettings ends a message with how to change where it goes.
func (w Where) writeSettings(b *strings.Builder, experiment string, own bool) {
	if own {
		fmt.Fprintf(b, "\nExperiment %s sends to the channels it was created with (gregale flag experiment create --notify).", experiment)
		return
	}
	fmt.Fprintf(b, "\nChange where these go: gregale flag notify set --channel ID %s; turn them off: gregale flag notify clear %s", w.flags(), w.flags())
}

// ExperimentInput is an experiment's current recommendation.
type ExperimentInput struct {
	Where
	Experiment, Flag string
	// Action, Variant, Summary and Commands are the results endpoint's
	// recommendation.
	Action, Variant, Summary string
	Commands                 []string
	// OwnChannels is set when the channels are the experiment's own.
	OwnChannels bool
}

// Experiment returns the notification for a recommendation that needs a
// human: a decision (ship_variant, keep_control) or a problem
// (rolled_back, fix_allocation). Other actions return false.
func Experiment(in ExperimentInput, channels []string) (state.FlagNotification, bool) {
	var kind, subject string
	switch in.Action {
	case flags.RecommendShipVariant:
		kind, subject = state.FlagNotifyExperimentDecision, fmt.Sprintf("Experiment %s: ship %s", in.Experiment, in.Variant)
	case flags.RecommendKeepControl:
		kind, subject = state.FlagNotifyExperimentDecision, fmt.Sprintf("Experiment %s: keep control", in.Experiment)
	case flags.RecommendRolledBack:
		kind, subject = state.FlagNotifyExperimentProblem, fmt.Sprintf("Experiment %s: rolled back by a guardrail", in.Experiment)
	case flags.RecommendFixAllocation:
		kind, subject = state.FlagNotifyExperimentProblem, fmt.Sprintf("Experiment %s: sample ratio mismatch", in.Experiment)
	default:
		return state.FlagNotification{}, false
	}
	if len(channels) == 0 {
		return state.FlagNotification{}, false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Experiment %s on flag %s in %s/%s: %s.", in.Experiment, in.Flag, in.Project, in.Environment, in.Summary)
	writeCommands(&b, in.Commands)
	fmt.Fprintf(&b, "\nResults: gregale flag experiment results %s %s", in.Experiment, in.flags())
	in.writeSettings(&b, in.Experiment, in.OwnChannels)
	return state.FlagNotification{
		AccountID: in.AccountID, OrgID: in.OrgID, EnvironmentID: in.EnvironmentID, Kind: kind,
		Key:     clip(fmt.Sprintf("experiment:%s:%s:%s:%s", in.EnvironmentID, in.Experiment, in.Action, in.Variant), 256),
		Subject: clip(subject, 256), Summary: clip(b.String(), 8192), ChannelIDs: slices.Clone(channels),
	}, true
}

// Digest returns the weekly lifecycle digest of the flags that need
// attention, once per ISO week of now (UTC). No flag needs attention:
// false.
func Digest(w Where, report []flags.FlagLifecycle, channels []string, now time.Time) (state.FlagNotification, bool) {
	var attention []flags.FlagLifecycle
	for _, f := range report {
		if len(f.Attention) > 0 {
			attention = append(attention, f)
		}
	}
	if len(attention) == 0 || len(channels) == 0 {
		return state.FlagNotification{}, false
	}
	year, week := now.UTC().ISOWeek()
	var b strings.Builder
	fmt.Fprintf(&b, "%d flag(s) in %s/%s need a decision:", len(attention), w.Project, w.Environment)
	for i, f := range attention {
		if i == maxDigestFlags {
			fmt.Fprintf(&b, "\n…and %d more: gregale flag stale list %s", len(attention)-i, w.flags())
			break
		}
		owner := f.Owner
		if owner == "" {
			owner = "no owner"
		}
		fmt.Fprintf(&b, "\n- %s (%s; %s)", f.Key, strings.Join(f.Attention, ", "), owner)
		if cmd := lifecycleCommand(w, f); cmd != "" {
			fmt.Fprintf(&b, ": %s", cmd)
		}
	}
	w.writeSettings(&b, "", false)
	return state.FlagNotification{
		AccountID: w.AccountID, OrgID: w.OrgID, EnvironmentID: w.EnvironmentID, Kind: state.FlagNotifyLifecycleDigest,
		Key:     fmt.Sprintf("lifecycle-digest:%s:%d-W%02d", w.EnvironmentID, year, week),
		Subject: clip(fmt.Sprintf("%d flag(s) in %s/%s need a decision", len(attention), w.Project, w.Environment), 256),
		Summary: clip(b.String(), 8192), ChannelIDs: slices.Clone(channels),
	}, true
}

// Expired returns one notification per flag whose expiry has passed, once
// per flag and expiry date.
func Expired(w Where, report []flags.FlagLifecycle, channels []string) []state.FlagNotification {
	if len(channels) == 0 {
		return nil
	}
	var out []state.FlagNotification
	for _, f := range report {
		if !slices.Contains(f.Attention, flags.AttentionExpired) {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Flag %s in %s/%s expired on %s.", f.Key, w.Project, w.Environment, f.ExpiresAt)
		if f.Owner != "" {
			fmt.Fprintf(&b, " Owner: %s.", f.Owner)
		}
		fmt.Fprintf(&b, "\nRemove it from code, then: gregale flag delete %s %s", f.Key, w.flags())
		fmt.Fprintf(&b, "\nOr keep it: gregale flag set %s --expires YYYY-MM-DD %s", f.Key, w.flags())
		w.writeSettings(&b, "", false)
		out = append(out, state.FlagNotification{
			AccountID: w.AccountID, OrgID: w.OrgID, EnvironmentID: w.EnvironmentID, Kind: state.FlagNotifyFlagExpired,
			Key:     clip(fmt.Sprintf("flag-expired:%s:%s:%s", w.EnvironmentID, f.Key, f.ExpiresAt), 256),
			Subject: clip(fmt.Sprintf("Flag %s expired", f.Key), 256), Summary: clip(b.String(), 8192),
			ChannelIDs: slices.Clone(channels),
		})
	}
	return out
}

// maxDigestFlags bounds the flags a digest lists.
const maxDigestFlags = 25

// lifecycleCommand is the command that resolves a flag's first reason.
func lifecycleCommand(w Where, f flags.FlagLifecycle) string {
	switch f.Attention[0] {
	case flags.AttentionExpired, flags.AttentionStale, flags.AttentionNeverEvaluated:
		return fmt.Sprintf("gregale flag delete %s %s", f.Key, w.flags())
	case flags.AttentionExpiresSoon:
		return fmt.Sprintf("gregale flag set %s --expires YYYY-MM-DD %s", f.Key, w.flags())
	case flags.AttentionNoOwner:
		return fmt.Sprintf("gregale flag set %s --owner TEAM %s", f.Key, w.flags())
	}
	return ""
}

func writeCommands(b *strings.Builder, commands []string) {
	for _, c := range commands {
		fmt.Fprintf(b, "\nNext: %s", c)
	}
}

// clip cuts s to at most n bytes on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("…")
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }
