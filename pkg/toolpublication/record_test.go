package toolpublication_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func TestAbandonedPublicationNeverBecomesAPlan(t *testing.T) {
	now := time.Now()
	record := api.ToolPublicationRecord{ID: strings.Repeat("a", 64), Status: "running", Deadline: now}
	if got := toolpublication.View(record, now); got.Status != "outcome_unknown" {
		t.Fatalf("abandoned status: %s", got.Status)
	}
	if got := toolpublication.View(record, now.Add(-time.Second)); got.Status != "running" {
		t.Fatalf("active status: %s", got.Status)
	}
	record.Status = "completed"
	if got := toolpublication.View(record, now.Add(time.Hour)); got.Status != "completed" {
		t.Fatal("durable completed evidence expired")
	}
}
