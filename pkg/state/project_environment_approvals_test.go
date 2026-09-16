package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemStoreProjectEnvironmentApprovalExactBindingAndExpiry(t *testing.T) {
	store := NewMemStore()
	now := time.Now().UTC()
	approval, err := store.CreateProjectEnvironmentApproval(context.Background(), ProjectEnvironmentApproval{
		AccountID:         "acct-1",
		ProjectSlug:       "checkout",
		EnvironmentSlug:   "production",
		PlanTokenHash:     "plan-hash",
		ApprovalTokenHash: "approval-hash",
		ExpiresAt:         now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateProjectEnvironmentApproval: %v", err)
	}
	if approval.ID == "" || approval.CreatedAt.IsZero() {
		t.Fatalf("approval metadata not assigned: %#v", approval)
	}
	if _, err := store.ProjectEnvironmentApprovalByToken(context.Background(), "acct-1", "checkout", "production", "plan-hash", "approval-hash"); err != nil {
		t.Fatalf("exact approval lookup: %v", err)
	}
	consumedAt := now.Add(10 * time.Second)
	consumed, err := store.ConsumeProjectEnvironmentApproval(context.Background(), "acct-1", "checkout", "production", "plan-hash", "approval-hash", consumedAt)
	if err != nil {
		t.Fatalf("consume approval: %v", err)
	}
	if consumed.ConsumedAt == nil || !consumed.ConsumedAt.Equal(consumedAt) {
		t.Fatalf("consumed approval timestamp = %v, want %v", consumed.ConsumedAt, consumedAt)
	}
	if _, err := store.ProjectEnvironmentApprovalByToken(context.Background(), "acct-1", "checkout", "production", "plan-hash", "approval-hash"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reused approval lookup = %v, want ErrNotFound", err)
	}
	loaded, err := store.ProjectEnvironmentApprovalByID(context.Background(), "acct-1", "checkout", "production", approval.ID)
	if err != nil {
		t.Fatalf("approval status lookup: %v", err)
	}
	if loaded.ConsumedAt == nil || !loaded.ConsumedAt.Equal(consumedAt) {
		t.Fatalf("loaded consumed approval = %#v", loaded)
	}
	if _, err := store.ProjectEnvironmentApprovalByToken(context.Background(), "acct-1", "checkout", "staging", "plan-hash", "approval-hash"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong environment lookup error = %v, want ErrNotFound", err)
	}

	expired, err := store.CreateProjectEnvironmentApproval(context.Background(), ProjectEnvironmentApproval{
		AccountID:         "acct-1",
		ProjectSlug:       "checkout",
		EnvironmentSlug:   "production",
		PlanTokenHash:     "expired-plan",
		ApprovalTokenHash: "expired-approval",
		ExpiresAt:         now.Add(-time.Second),
	})
	if err != nil {
		t.Fatalf("Create expired approval: %v", err)
	}
	if _, err := store.ProjectEnvironmentApprovalByToken(context.Background(), "acct-1", "checkout", "production", "expired-plan", "expired-approval"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired approval lookup = %v, want ErrNotFound (id %s)", err, expired.ID)
	}
	if _, err := store.ConsumeProjectEnvironmentApproval(context.Background(), "acct-1", "checkout", "production", "expired-plan", "expired-approval", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired approval consume = %v, want ErrNotFound", err)
	}
}
