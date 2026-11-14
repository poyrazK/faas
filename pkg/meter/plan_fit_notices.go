package meter

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// RunPlanFitNoticesOnce emails each billing subject whose apps stopped
// fitting its plan (billing review H17-4). schedd already parks and refuses
// to wake those apps (ADR-20261103131742, state.AppsOverPlan); this tells the
// customer which ones and what lets them wake. One email goes out per
// (plan, over-plan app set): the set is remembered in plan_fit_notices and
// forgotten once the subject fits again, so a later excess is mailed anew.
// A send failure leaves the set unrecorded, so the next tick retries.
func (l *Loop) RunPlanFitNoticesOnce(ctx context.Context) error {
	notices, ok := l.store.(state.PlanFitNoticeStore)
	if !ok || l.mailer == nil {
		return nil
	}
	subjects, err := state.ListBillingResourceAccounts(ctx, l.store)
	if err != nil {
		return fmt.Errorf("meter: plan fit notices: list subjects: %w", err)
	}
	var failed []error
	for _, subject := range subjects {
		if err := l.planFitNotice(ctx, notices, subject); err != nil {
			failed = append(failed, fmt.Errorf("plan fit notice %s: %w", subject.ID, err))
		}
	}
	return errors.Join(failed...)
}

func (l *Loop) planFitNotice(ctx context.Context, notices state.PlanFitNoticeStore, subject state.Account) error {
	if subject.Status == state.AccountDeletedPending {
		return nil
	}
	limits, ok := api.LimitsFor(subject.Plan)
	if !ok {
		return nil
	}
	apps, err := state.PlanSubjectApps(ctx, l.store, subject)
	if err != nil {
		return err
	}
	over := state.AppsOverPlan(apps, limits)
	if len(over) == 0 {
		return notices.ClearPlanFitNotice(ctx, subject.ID)
	}
	ids := make([]string, 0, len(over))
	for id := range over {
		ids = append(ids, id)
	}
	digest := state.PlanFitDigest(ids)
	prev, found, err := notices.PlanFitNotice(ctx, subject.ID)
	if err != nil {
		return err
	}
	if found && prev.Plan == subject.Plan && prev.AppsDigest == digest {
		return nil
	}
	// An organization without a billing email is told through its owner
	// (billing review R10-3).
	to := billingMailTo(ctx, l.store, subject)
	if to == "" {
		return nil // nobody to tell; retried once the subject has an address
	}
	parked := make([]mail.PlanFitApp, 0, len(over))
	for _, app := range apps {
		if o, isOver := over[app.ID]; isOver {
			parked = append(parked, mail.PlanFitApp{Slug: app.Slug, RAMMB: o.RAMMB, PastAppLimit: o.DeployedApps > 0})
		}
	}
	sort.Slice(parked, func(i, j int) bool { return parked[i].Slug < parked[j].Slug })
	planName := planDisplayName(subject.Plan)
	subj, body := mail.PlanFitBody(to, planName, limits.DeployedApps, limits.RAMMB, parked)
	msg := mail.Message{To: []string{to}, Subject: subj, TextBody: body,
		MessageID: fmt.Sprintf("%s:plan_fit:%s:%s", subject.ID, subject.Plan, digest[:16])}
	if err := l.mailer.Send(ctx, msg); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	return notices.PutPlanFitNotice(ctx, subject.ID, state.PlanFitNotice{Plan: subject.Plan, AppsDigest: digest})
}

// planDisplayName is a plan's name as the plans page writes it ("Free").
func planDisplayName(plan api.Plan) string {
	s := string(plan)
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
