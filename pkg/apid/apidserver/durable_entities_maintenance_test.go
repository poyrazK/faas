// adr: 712
package apidserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/durable"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

func (b *entityTestBucket) ListEntityObjects(ctx context.Context, prefix, cursor string, limit int32) (durableentity.CleanupObjects, error) {
	if err := ctx.Err(); err != nil {
		return durableentity.CleanupObjects{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var keys []string
	for key := range b.objects {
		if strings.HasPrefix(key, prefix) && key > cursor {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	page := durableentity.CleanupObjects{Keys: keys}
	if len(keys) > int(limit) {
		page.Keys = keys[:limit]
		page.NextCursor = page.Keys[len(page.Keys)-1]
	}
	return page, nil
}

func (b *entityTestBucket) DeleteEntityObject(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.objects, key)
	return nil
}

func TestDurableEntityMaintenanceRequiresPreviewAndStopsWorker(t *testing.T) {
	s := &apidserver.Server{}
	if err := s.ConfigureDurableEntities(t.Context(), func(key string) string {
		if key == "FAAS_DURABLE_ENTITY_MAINTENANCE_ENABLED" {
			return "1"
		}
		return ""
	}); err == nil {
		t.Fatal("maintenance enabled without preview")
	}
	(*s.Durable()).RunMaintenance(t.Context()) // disabled returns immediately
	e, _, _, _ := entityAPIFixture(t, false)
	(*e.s.Durable()).MaintenanceEnabled = true
	registry := prometheus.NewRegistry()
	(*e.s.Durable()).Metrics = durable.NewMetrics(registry, "apid")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { (*e.s.Durable()).RunMaintenance(ctx); close(done) }()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for apidserver.DurableMetric(t, registry, "apid_durable_entity_maintenance_last_sweep_timestamp_seconds") == 0 {
		select {
		case <-deadline.C:
			t.Fatal("maintenance did not perform its initial rotation")
		case <-poll.C:
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("maintenance failed to stop")
	}
}

func TestDurableEntityAutomaticMaintenancePreservesRuntimeReplayAndAlarms(t *testing.T) {
	e, _, dispatch, _ := entityAPIFixture(t, false)
	(*e.s.Durable()).AlarmsEnabled = true
	registry := prometheus.NewRegistry()
	(*e.s.Durable()).Metrics = durable.NewMetrics(registry, "apid")
	alarm := scheduleAPIAlarm(t, e, entityRequest("first"))
	for i := range 12 {
		request := entityRequest(fmt.Sprintf("work-%d", i))
		request.Payload, _ = json.Marshal(struct {
			Delta int       `json:"delta"`
			At    time.Time `json:"alarm_at"`
		}{1, alarm.At})
		entityAPIResult(t, e.do(t, http.MethodPost, "/v1/apps/entity-counter/entities/invoke", request, nil))
	}
	deleted := 0
	for range 10 {
		result, err := (*e.s.Durable()).Engine.MaintenanceStep(t.Context(), (*e.s.Durable()).Owner, (*e.s.Durable()).Apps)
		if err != nil || result.Failed != 0 || result.Cleanup.Failed != 0 {
			t.Fatal(result, err)
		}
		(*e.s.Durable()).Metrics.ObserveMaintenance(result, err)
		deleted += result.Cleanup.Deleted
	}
	if deleted == 0 || apidserver.DurableMetric(t, registry, "apid_durable_entity_cleanup_objects_total", "outcome", "deleted") != float64(deleted) {
		t.Fatal("maintenance did not reclaim superseded runtime state", deleted)
	}
	before := dispatch.calls.Load()
	request := entityRequest("work-0")
	request.Payload, _ = json.Marshal(struct {
		Delta int       `json:"delta"`
		At    time.Time `json:"alarm_at"`
	}{1, alarm.At})
	replayed := entityAPIResult(t, e.do(t, http.MethodPost, "/v1/apps/entity-counter/entities/invoke", request, nil))
	if !replayed.Replayed || replayed.Version != 2 || dispatch.calls.Load() != before {
		t.Fatal("reclaimed original receipt", replayed)
	}
	page, err := (*e.s.Durable()).Engine.ScanDueAlarms(t.Context(), "")
	if err != nil || len(page.Alarms) != 1 || page.Alarms[0].Version != 13 {
		t.Fatal("lost alarm", page, err)
	}
	if err := (*e.s.Durable()).DeliverAlarm(t.Context(), page.Alarms[0]); err != nil {
		t.Fatal(err)
	}
	if apidserver.DurableMetric(t, registry, "apid_durable_entity_operations_total", "operation", "invoke", "outcome", "replay") != 1 || apidserver.DurableMetric(t, registry, "apid_durable_entity_operations_total", "operation", "alarm", "outcome", "success") != 1 {
		t.Fatal("invocation or alarm outcome was not observed")
	}
	view, err := (*e.s.Durable()).Engine.Read(t.Context(), alarm.Entity)
	if err != nil || view.Version != 14 || view.AlarmAt != nil {
		t.Fatal(view, err)
	}
}
