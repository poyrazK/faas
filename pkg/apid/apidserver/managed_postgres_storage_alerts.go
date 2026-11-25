package apidserver

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/dbstorage"
)

func (s *server) runManagedPostgresStorageAlerts(ctx context.Context, pool *pgxpool.Pool) {
	if s.managedPostgres == nil || s.mailer == nil || pool == nil {
		return
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	var pruneAfter time.Time
	for {
		if err := dbstorage.DeliverAlerts(ctx, pool, s.sendManagedPostgresStorageAlert); err != nil && ctx.Err() == nil {
			s.log.Warn("managed postgres storage alert delivery failed")
		}
		if time.Now().After(pruneAfter) && ctx.Err() == nil {
			pruneAfter = time.Now().Add(time.Hour)
			if err := dbstorage.PruneAlerts(ctx, pool); err != nil && ctx.Err() == nil {
				s.log.Warn("managed postgres storage alert cleanup failed")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *server) sendManagedPostgresStorageAlert(ctx context.Context, alert dbstorage.Alert, recipient dbstorage.AlertRecipient) error {
	var title, impact string
	switch alert.State {
	case "low_space":
		title, impact = "Low storage", "The primary was still writable at the time of this sample."
	case "read_only":
		title, impact = "Database entered read-only mode", "The storage guard put the primary in read-only mode. Reads can continue while writes are blocked."
	case "recovered":
		title, impact = "Storage recovered", "A fresh sample confirmed that the primary is writable and has recovered from low storage."
	}
	const mib = 1 << 20
	body := fmt.Sprintf("Gregale recorded a storage alert for PostgreSQL database %s at %s.\n\nFree space was %.1f MiB out of %.1f MiB. %s",
		alert.DatabaseName, alert.SampledAt.UTC().Format("2006-01-02 15:04 UTC"), float64(alert.FreeBytes)/mib, float64(alert.TotalBytes)/mib, impact)
	if growth := storageAlertGrowthMessage(alert.GrowthReason); growth != "" {
		body += "\n\n" + growth
	}
	body += "\n\nReview the latest sample and alert history in Gregale's database status."
	address := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(recipient.Email))))
	return s.mailer.Send(ctx, Message{To: []string{recipient.Email}, Subject: title + " for " + alert.DatabaseName,
		TextBody: body, MessageID: fmt.Sprintf("postgres-storage:%s:%x", alert.ID, address[:16])})
}

func storageAlertGrowthMessage(reason string) string {
	switch reason {
	case "resize_queued":
		return "A storage increase has already been requested."
	case "manual_resize_required":
		return "Free space or request a manual storage increase to make the database writable again."
	case "disabled":
		return "Automatic storage growth is off. Free space or request a storage increase."
	case "ceiling_reached":
		return "The database has reached its automatic growth ceiling. Free space or raise the ceiling."
	case "storage_allowance":
		return "The plan's remaining storage allowance blocked automatic growth. Free space or increase the available allowance."
	case "resize_conflict":
		return "Another database operation blocked automatic growth. Check the latest operation before resizing."
	case "resize_unavailable", "node_capacity":
		return "Storage growth was unavailable when Gregale last checked. Check the latest database status."
	case "waiting_for_idle":
		return "Automatic storage growth can start when the database next goes idle."
	case "waiting_for_maintenance_window":
		return "Automatic storage growth can start during the configured maintenance window."
	case "maintenance_window_required":
		return "Automatic storage growth needs a maintenance window for this database."
	default:
		return ""
	}
}
