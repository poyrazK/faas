package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// buildConsumerSignupRequest validates the sign-up verbs of `gregale
// consumers` (ADR-20261021143000); handled is false for every other verb.
func buildConsumerSignupRequest(verb string, f consumerFlags) (any, bool, error) {
	if verb != "signup-set" {
		return nil, false, nil
	}
	if strings.TrimSpace(f.displayName) == "" {
		return nil, true, errors.New("--display-name is required")
	}
	if f.trialDays < 0 || f.dailyLimit < 0 {
		return nil, true, errors.New("--trial-days and --daily-limit cannot be negative")
	}
	enabled := !f.disabled
	req := api.PutConsumerSignupRequest{Enabled: &enabled, DisplayName: strings.TrimSpace(f.displayName), TermsURL: f.termsURL,
		ReturnURL: f.returnURL, TrialDays: f.trialDays, DailyLimit: f.dailyLimit, CustomerPlanChanges: f.customerPlanChanges,
		CustomerClosing: f.customerClosing}
	if f.keyScopes != "" {
		req.KeyScopes = splitList(f.keyScopes)
	}
	return req, true, nil
}

// splitList returns the trimmed, non-empty entries of a comma-separated
// list, keeping their case.
func splitList(text string) []string {
	var out []string
	for _, part := range strings.Split(text, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// callConsumerSignup calls the sign-up endpoints; handled is false for
// every other verb.
func callConsumerSignup(ctx context.Context, client *Client, verb string, args []string, f consumerFlags, request any) (any, bool, error) {
	slug := args[0]
	switch verb {
	case "signup":
		out, err := client.GetConsumerSignup(ctx, slug)
		return out, true, err
	case "signup-set":
		req := request.(api.PutConsumerSignupRequest)
		for _, name := range splitList(f.plans) {
			id, err := resolvePlanID(ctx, client, slug, name)
			if err != nil {
				return nil, true, err
			}
			req.PlanIDs = append(req.PlanIDs, id)
		}
		out, err := client.PutConsumerSignup(ctx, slug, req)
		return out, true, err
	case "signup-delete":
		err := client.DeleteConsumerSignup(ctx, slug)
		if err != nil {
			return nil, true, err
		}
		return map[string]string{"status": "sign-up turned off"}, true, nil
	case "signups":
		out, err := client.ListConsumerSignups(ctx, slug)
		return out, true, err
	}
	return nil, false, nil
}

func printConsumerSignupResult(tw *tabwriter.Writer, out any) bool {
	switch v := out.(type) {
	case api.ConsumerSignupSettingsResponse:
		state := "open"
		if !v.Enabled {
			state = "closed: the page says this API does not take sign-ups"
		}
		plans := "default plan"
		if len(v.PlanIDs) > 0 {
			plans = strings.Join(v.PlanIDs, ", ")
		}
		_, _ = fmt.Fprintf(tw, "Sign-up\t%s\nPage\t%s\nName\t%s\nPlans\t%s\nKey scopes\t%s\nDaily limit\t%d\n", state, v.PageURL,
			v.DisplayName, plans, strings.Join(v.KeyScopes, ","), v.DailyLimit)
		if v.TrialDays > 0 {
			_, _ = fmt.Fprintf(tw, "Trial\t%d days\n", v.TrialDays)
		}
		if v.TermsURL != "" {
			_, _ = fmt.Fprintf(tw, "Terms\t%s\n", v.TermsURL)
		}
		if v.ReturnURL != "" {
			_, _ = fmt.Fprintf(tw, "Return\t%s\n", v.ReturnURL)
		}
		_, _ = fmt.Fprintf(tw, "Customers may\t%s\n", customerSelfService(v))
	case api.ConsumerSignupListResponse:
		_, _ = fmt.Fprintln(tw, "ID\tSTATUS\tEMAIL\tNAME\tCONSUMER\tCREATED")
		for _, s := range v.Signups {
			status := s.Status
			if !s.Livemode {
				status += " (test)"
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, status, s.Email, s.Name, dashIfEmpty(s.ConsumerID),
				s.CreatedAt.Format(time.RFC3339))
		}
	default:
		return false
	}
	return true
}

// customerSelfService names what customers may do with their own key
// (ADR-20261021183000).
func customerSelfService(v api.ConsumerSignupSettingsResponse) string {
	var can []string
	if v.CustomerPlanChanges {
		can = append(can, "change plans")
	}
	if v.CustomerClosing {
		can = append(can, "close their account")
	}
	can = append(can, "update their card in the Stripe portal")
	return strings.Join(can, ", ")
}
