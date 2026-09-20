package daemonunitspec

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
)

// TestNotifyUnitsDeclareWatchdog (ADR-190) pins that every Type=notify
// daemon unit carries a WatchdogSec and that Type=simple units do not:
// a simple unit never sends READY=1, so systemd would treat a missing
// WATCHDOG=1 as a stall from the first second.
func TestNotifyUnitsDeclareWatchdog(t *testing.T) {
	for _, entry := range UnitEntries() {
		t.Run(entry.Name, func(t *testing.T) {
			u := entry.Unit()
			switch u.Type {
			case "notify":
				if u.WatchdogSec == "" {
					t.Fatalf("%s is Type=notify but has no WatchdogSec", entry.Name)
				}
				d, err := time.ParseDuration(u.WatchdogSec)
				if err != nil || d < 30*time.Second {
					t.Fatalf("%s WatchdogSec=%q must parse and be >= 30s", entry.Name, u.WatchdogSec)
				}
				if u.Restart != "on-failure" {
					t.Fatalf("%s: a watchdog abort only restarts under Restart=on-failure, got %q", entry.Name, u.Restart)
				}
			default:
				if u.WatchdogSec != "" {
					t.Fatalf("%s is Type=%s and must not set WatchdogSec", entry.Name, u.Type)
				}
			}
		})
	}
}

// TestScheddWatchdogOutlastsMainLoopBudget pins the relationship the
// schedd unit comment promises: the watchdog interval is at least the
// main loop's stall budget, so a legitimately long Prime cannot be
// mistaken for a wedge before the loop itself reports the stall.
func TestScheddWatchdogOutlastsMainLoopBudget(t *testing.T) {
	d, err := time.ParseDuration(UnitSchedd().WatchdogSec)
	if err != nil {
		t.Fatal(err)
	}
	if d < sched.MainLoopBudget {
		t.Fatalf("schedd WatchdogSec %s < sched.MainLoopBudget %s", d, sched.MainLoopBudget)
	}
}
