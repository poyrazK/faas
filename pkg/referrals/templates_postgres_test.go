package referrals_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/referrals"
)

func TestSalesTemplatesPermissionsVersionsAndPersonalization(t *testing.T) {
	pool, s, owner, other := pipelineFixture(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261111125852301_sales_templates.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(raw), "-- +goose Down")
	if _, err = pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	manager := referrals.Scope{OwnerID: owner, All: true}
	rep := referrals.Scope{OwnerID: other}
	former := referrals.Scope{OwnerID: owner}
	lead, err := s.SaveProspect(ctx, manager, owner, "", prospect(other, "maya@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	in := messageInput()
	if _, err = s.SaveMessageTemplate(ctx, rep, "", in); !errors.Is(err, referrals.ErrNotFound) {
		t.Fatal("rep created template", err)
	}
	id, err := s.SaveMessageTemplate(ctx, manager, "", in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MessageTemplate(ctx, rep, id); !errors.Is(err, referrals.ErrNotFound) {
		t.Fatal("draft visible", err)
	}
	rows, err := s.MessageTemplates(ctx, rep, referrals.TemplateFilter{Limit: 25})
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	if _, err = s.MessageTemplateSource(ctx, rep, lead, id, 1); !errors.Is(err, referrals.ErrNotFound) {
		t.Fatal("draft previewed", err)
	}
	in.Status = "active"
	in.Version = 1
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, e := s.SaveMessageTemplate(ctx, manager, id, in); results <- e })
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, referrals.ErrConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
	if _, err = s.MessageTemplateSource(ctx, rep, lead, id, 1); !errors.Is(err, referrals.ErrConflict) {
		t.Fatal("stale preview", err)
	}
	// Manager code must never substitute for the assigned rep's code.
	own, err := s.EnsureLink(ctx, owner, "sales")
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.MessageTemplateSource(ctx, manager, lead, id, 2)
	if err != nil || source.Lead.ReferralCode != "" || source.Lead.OwnerEmail != "other@example.com" {
		t.Fatal(source, err)
	}
	link, err := s.EnsureLink(ctx, other, "sales")
	if err != nil {
		t.Fatal(err)
	}
	source, err = s.MessageTemplateSource(ctx, rep, lead, id, 2)
	if err != nil || source.Lead.ReferralCode != link.Code || source.Lead.ReferralCode == own.Code {
		t.Fatal(source, err)
	}
	p, err := referrals.PersonalizeMessage(source.Template.MessageTemplateInput, source.Lead)
	if err != nil || len(p.Missing) != 0 || !strings.Contains(p.Body, link.Code) {
		t.Fatal(p, err)
	}
	if _, err = s.MessageTemplateSource(ctx, former, lead, id, 2); !errors.Is(err, referrals.ErrNotFound) {
		t.Fatal("other owner's lead previewed", err)
	}
	if _, err = s.SetLinkActive(ctx, rep, link.Code, false); err != nil {
		t.Fatal(err)
	}
	source, err = s.MessageTemplateSource(ctx, rep, lead, id, 2)
	if err != nil || source.Lead.ReferralCode != "" {
		t.Fatal("paused code offered", source, err)
	}
	if _, err = s.SetLinkActive(ctx, rep, link.Code, true); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"status='disabled'", "email_verified_at=NULL", "mfa_enrolled_at=NULL"} {
		if _, err = pool.Exec(ctx, "UPDATE accounts SET "+column+" WHERE id=$1", other); err != nil {
			t.Fatal(err)
		}
		source, err = s.MessageTemplateSource(ctx, manager, lead, id, 2)
		if err != nil || source.Lead.ReferralCode != "" {
			t.Fatal("ineligible code offered", source, err)
		}
		if _, err = pool.Exec(ctx, "UPDATE accounts SET status='active',email_verified_at=now(),mfa_enrolled_at=now() WHERE id=$1", other); err != nil {
			t.Fatal(err)
		}
	}
	in.Version = 2
	in.Status = "archived"
	if _, err = s.SaveMessageTemplate(ctx, manager, id, in); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MessageTemplateSource(ctx, rep, lead, id, 3); !errors.Is(err, referrals.ErrNotFound) {
		t.Fatal("archive offered", err)
	}
	if _, err = s.MessageTemplate(ctx, manager, id); err != nil {
		t.Fatal(err)
	}
	// Assignment is checked anew on every preview.
	in.Status = "active"
	in.Version = 3
	if _, err = s.SaveMessageTemplate(ctx, manager, id, in); err != nil {
		t.Fatal(err)
	}
	input := prospect(owner, "maya@example.com")
	input.Version = 1
	if _, err = s.SaveProspect(ctx, manager, owner, lead, input); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MessageTemplateSource(ctx, rep, lead, id, 4); !errors.Is(err, referrals.ErrNotFound) {
		t.Fatal("former owner previewed", err)
	}
	// Pagination does not reveal drafts and archives to reps.
	for range 27 {
		if _, err = s.SaveMessageTemplate(ctx, manager, "", messageInput()); err != nil {
			t.Fatal(err)
		}
	}
	rows, err = s.MessageTemplates(ctx, manager, referrals.TemplateFilter{Limit: 25})
	if err != nil || len(rows) != 25 {
		t.Fatal(len(rows), err)
	}
	second, err := s.MessageTemplates(ctx, manager, referrals.TemplateFilter{Limit: 25, After: rows[24].ID})
	if err != nil || len(second) != 3 {
		t.Fatal(len(second), err)
	}
	rows, err = s.MessageTemplates(ctx, rep, referrals.TemplateFilter{Limit: 25})
	if err != nil || len(rows) != 1 || rows[0].ID != id {
		t.Fatal(rows, err)
	}
}
