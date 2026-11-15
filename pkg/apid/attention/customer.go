package attention

// Customer view of attention signals (ADR-20261114190002). The account
// health page and GET /v1/account/health show a customer their own open
// signals of the kinds below, worded for them. Every other kind stays
// internal: refused_burst, domain_stuck and payment_failing repeat sections
// the health page already has (refused requests, custom domains, the
// past-due restriction), and boot_failing includes platform causes the
// customer cannot act on. A kind missing from customerWording is never
// shown, so a new kind stays internal until it is given wording here.

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// CustomerKinds lists the kinds customers see, in display order.
func CustomerKinds() []string {
	out := make([]string, 0, len(customerWording))
	for _, kind := range api.AttentionKinds() {
		if _, ok := customerWording[kind]; ok {
			out = append(out, kind)
		}
	}
	return out
}

// customerWording builds the customer text for one signal from its subject
// and observed count. It never uses the signal's detail, which is written
// for staff.
var customerWording = map[string]func(s api.ObsAttentionSignal) api.AccountHealthSignal{
	api.AttentionKindDeployFailing: func(s api.ObsAttentionSignal) api.AccountHealthSignal {
		return api.AccountHealthSignal{
			Title: "Deployments of " + s.Subject + " keep failing",
			Detail: fmt.Sprintf("%s failed in the last %s and none has succeeded since. "+
				"The app keeps serving its last successful release, if it has one.",
				plural(s.Observed, "deployment"), window(api.AttentionDeployWindow)),
			Action: "Open the app", ActionURL: appURL(s.Subject),
		}
	},
	api.AttentionKindAppOOM: func(s api.ObsAttentionSignal) api.AccountHealthSignal {
		return api.AccountHealthSignal{
			Title: s.Subject + " keeps running out of memory",
			Detail: fmt.Sprintf("It was stopped for exceeding its memory limit %s in the last %s. "+
				"Reduce its memory use, or move to a plan with more memory per app.",
				times(s.Observed), window(api.AttentionOOMWindow)),
			Action: "Compare plans", ActionURL: "/dashboard/billing",
		}
	},
	api.AttentionKindJobsFailing: func(s api.ObsAttentionSignal) api.AccountHealthSignal {
		return api.AccountHealthSignal{
			Title: "Scheduled job " + s.Subject + " keeps failing",
			Detail: fmt.Sprintf("Its last %s failed. Run gregale jobs runs %s to see the runs and their logs.",
				plural(s.Observed, "scheduled run"), s.Subject),
		}
	},
	api.AttentionKindLimitPressure: func(s api.ObsAttentionSignal) api.AccountHealthSignal {
		return api.AccountHealthSignal{
			Title: "You keep reaching a plan limit",
			Detail: fmt.Sprintf("Requests were refused with %s on %s of the last %s. "+
				"A higher plan raises the limit.", s.Subject, plural(s.Observed, "day"), window(api.AttentionLimitWindow)),
			Action: "Compare plans", ActionURL: "/dashboard/billing",
		}
	},
}

// ForCustomer maps open signals to the customer view, dropping kinds without
// customer wording and signals that are not open. A signal staff dismissed
// is hidden from the customer too.
func ForCustomer(signals []api.ObsAttentionSignal) []api.AccountHealthSignal {
	out := []api.AccountHealthSignal{}
	for _, s := range signals {
		word, ok := customerWording[s.Kind]
		if !ok || s.State != api.AttentionStateOpen || s.Subject == "" {
			continue
		}
		view := word(s)
		view.Kind, view.Subject, view.Since = s.Kind, s.Subject, s.FirstSeenAt.UTC()
		out = append(out, view)
	}
	return out
}

// OpenForCustomer returns the account's open signals in customer wording.
func (e *Evaluator) OpenForCustomer(ctx context.Context, accountID string) ([]api.AccountHealthSignal, error) {
	if e == nil {
		return []api.AccountHealthSignal{}, nil
	}
	signals, err := e.List(ctx, Filter{AccountID: accountID, States: []string{api.AttentionStateOpen}})
	if err != nil {
		return []api.AccountHealthSignal{}, err
	}
	return ForCustomer(signals), nil
}

// CountOpenForCustomer counts the signals OpenForCustomer would return,
// with one indexed count, for the dashboard overview banner.
func (e *Evaluator) CountOpenForCustomer(ctx context.Context, accountID string) (int, error) {
	if e == nil {
		return 0, nil
	}
	var n int
	err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM customer_attention_signals
		WHERE account_id = $1::uuid AND state = $2 AND kind = ANY($3) AND subject <> ''`,
		accountID, api.AttentionStateOpen, CustomerKinds()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("attention: count customer signals: %w", err)
	}
	return n, nil
}

func appURL(slug string) string {
	if !slugPattern.MatchString(slug) {
		return "/dashboard/apps"
	}
	return "/dashboard/apps/" + url.PathEscape(slug)
}

func plural(n int64, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.FormatInt(n, 10) + " " + noun + "s"
}

func times(n int64) string {
	if n == 1 {
		return "once"
	}
	return strconv.FormatInt(n, 10) + " times"
}

// window renders a detection window as "24 hours" or "7 days".
func window(d time.Duration) string {
	if d >= 48*time.Hour && d%(24*time.Hour) == 0 {
		return strconv.Itoa(int(d/(24*time.Hour))) + " days"
	}
	return plural(int64(d/time.Hour), "hour")
}
