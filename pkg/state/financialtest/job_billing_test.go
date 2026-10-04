package financialtest

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
)

// adr: 530 — closed-minute job billing must include completed and deleted jobs.
func TestFinancialPostgresJobBillingWindow(t *testing.T) {
	store, pool, ctx := financialPostgres(t)
	start, end := financialPeriod()
	minute := start.Add(time.Hour)
	node := financialLocalNode(t, ctx, store)
	for _, tc := range []struct {
		name, terminal string
		start, stop    time.Duration
		deleted        bool
		seconds        int64
	}{
		{"short_completed", "stopped", 10 * time.Second, 50 * time.Second, false, 40},
		{"failed_final_minute", "failed", -20 * time.Second, 40 * time.Second, false, 40},
		{"deleted_completed", "stopped", 10 * time.Second, 50 * time.Second, true, 40},
		{"still_running", "", 10 * time.Second, 0, false, 50},
		{"finished_before_window", "stopped", -50 * time.Second, -10 * time.Second, false, 0},
		{"starts_after_window", "", 70 * time.Second, 0, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := financialAccountWithContext(t, ctx, store)
			job, err := store.JobCreate(ctx, a.ID, "billing-job", "batch", "registry.example/job:v1", []string{"/job"}, 256, 60, 1, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			instance, err := store.CreateJobInstance(ctx, uuid.NewString(), job.ID, uuid.NewString(), 0, "running", 256, node, "")
			if err != nil {
				t.Fatal(err)
			}
			var stopped any
			if tc.terminal != "" {
				if err := store.UpdateInstanceStateToTerminal(ctx, instance.ID, tc.terminal, time.Now()); err != nil {
					t.Fatal(err)
				}
				stopped = minute.Add(tc.stop)
			}
			if _, err := pool.Exec(ctx, `update instance_billing_intervals set started_at=$2, ended_at=$3 where instance_id=$1`, instance.ID, minute.Add(tc.start), stopped); err != nil {
				t.Fatal(err)
			}
			if tc.deleted {
				if deleted, live, err := store.JobSoftDelete(ctx, job.ID); err != nil || !deleted || live {
					t.Fatalf("delete completed job: deleted=%t live=%t err=%v", deleted, live, err)
				}
			}
			sampler := meter.NewSampler(store, nil, func() time.Time { return minute.Add(90 * time.Second) })
			for range 2 {
				if _, err := sampler.SampleJobsAndRoll(ctx); err != nil {
					t.Fatal(err)
				}
			}
			head, err := store.FinancialEvidenceHead(ctx, a.ID, start, end)
			if err != nil {
				t.Fatal(err)
			}
			rows := financialRowsWithContext(t, ctx, store, a.ID, head)
			if tc.seconds == 0 {
				if len(rows) != 0 {
					t.Fatalf("billed outside residency: %+v", rows)
				}
				return
			}
			want := int64(api.BillableRAMMBWithSidecars(256, nil)) * tc.seconds
			if len(rows) != 1 || rows[0].InstanceID != instance.ID || rows[0].Evidence.Attribution.JobID != job.ID || rows[0].Evidence.Quantity != want || !rows[0].Evidence.Start.Equal(minute) {
				t.Fatalf("lost or duplicated closed-minute job usage: %+v; want %d MB-seconds", rows, want)
			}
		})
	}
}
