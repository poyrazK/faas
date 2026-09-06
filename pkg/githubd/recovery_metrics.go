package githubd

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// RecoveryCollector exports the durable delivery and Check Run outboxes
// without a cardinality-bearing delivery/deployment label.
type RecoveryCollector struct {
	pool        *pgxpool.Pool
	items       *prometheus.Desc
	oldest      *prometheus.Desc
	collectFail *prometheus.Desc
}

func NewRecoveryCollector(pool *pgxpool.Pool) *RecoveryCollector {
	return &RecoveryCollector{
		pool: pool,
		items: prometheus.NewDesc("githubd_recovery_queue_items",
			"Actionable durable GitHub recovery work by queue and state.", []string{"queue", "status"}, nil),
		oldest: prometheus.NewDesc("githubd_recovery_oldest_actionable_seconds",
			"Age of the oldest pending, processing, or dead GitHub recovery item.", []string{"queue", "status"}, nil),
		collectFail: prometheus.NewDesc("githubd_recovery_collect_error",
			"1 when the last GitHub recovery queue metric collection failed.", nil, nil),
	}
}

func (c *RecoveryCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.items
	ch <- c.oldest
	ch <- c.collectFail
}

func (c *RecoveryCollector) Collect(ch chan<- prometheus.Metric) {
	type key struct{ queue, status string }
	counts := make(map[key]float64)
	ages := make(map[key]float64)
	queues := []string{"webhook_delivery", "check_update"}
	statuses := []string{"pending", "processing", "dead"}
	for _, queue := range queues {
		for _, status := range statuses {
			counts[key{queue, status}] = 0
			ages[key{queue, status}] = 0
		}
	}

	failed := 0.0
	if c.pool == nil {
		failed = 1
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		rows, err := c.pool.Query(ctx, `
			select queue, status, count(*),
			       extract(epoch from greatest(interval '0', now() - min(updated_at)))
			from (
				select 'webhook_delivery'::text as queue, status, updated_at
				from github_webhook_deliveries
				where status in ('pending', 'processing', 'dead')
				union all
				select 'check_update'::text as queue, status, updated_at
				from github_check_updates
				where status in ('pending', 'processing', 'dead')
			) recovery
			group by queue, status`)
		if err != nil {
			failed = 1
		} else {
			for rows.Next() {
				var queue, status string
				var count int64
				var age float64
				if err := rows.Scan(&queue, &status, &count, &age); err != nil {
					failed = 1
					break
				}
				counts[key{queue, status}] = float64(count)
				ages[key{queue, status}] = age
			}
			if rows.Err() != nil {
				failed = 1
			}
			rows.Close()
		}
	}

	for item, value := range counts {
		ch <- prometheus.MustNewConstMetric(c.items, prometheus.GaugeValue, value, item.queue, item.status)
	}
	for item, value := range ages {
		ch <- prometheus.MustNewConstMetric(c.oldest, prometheus.GaugeValue, value, item.queue, item.status)
	}
	ch <- prometheus.MustNewConstMetric(c.collectFail, prometheus.GaugeValue, failed)
}
