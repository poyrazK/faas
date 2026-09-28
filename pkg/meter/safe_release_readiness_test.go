package meter

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/canary"
	"github.com/onebox-faas/faas/pkg/safedeploy"
)

// adr: 122 — canary admission requires live progression and recovery workers.
func TestSafeReleaseReadinessRequiresBothSuccessfulRecentTicks(t *testing.T) {
	now := time.Now()
	loop := &Loop{
		cfg:         &Config{CanaryEvalInterval: 30 * time.Second, SafeDeployInterval: 30 * time.Second},
		lastTick:    make(map[string]time.Time),
		lastTickErr: make(map[string]string),
	}
	if loop.SafeReleaseReadiness(now).Healthy {
		t.Fatal("unwired workers reported healthy")
	}
	loop.canaryProgression = &canary.Progression{}
	loop.safedeploy = &safedeploy.Orchestrator{}
	if loop.SafeReleaseReadiness(now).Healthy {
		t.Fatal("workers without a tick reported healthy")
	}
	loop.recordTick("canary_progression", now, nil)
	if loop.SafeReleaseReadiness(now).Healthy {
		t.Fatal("one successful tick reported healthy")
	}
	loop.recordTick("safedeploy", now, nil)
	if !loop.SafeReleaseReadiness(now).Healthy {
		t.Fatal("both successful ticks reported unhealthy")
	}
	loop.recordTick("safedeploy", now.Add(time.Second), errors.New("recovery worker failed"))
	if loop.SafeReleaseReadiness(now.Add(time.Second)).Healthy {
		t.Fatal("failed recovery tick reported healthy")
	}
	loop.recordTick("safedeploy", now.Add(2*time.Second), nil)
	if loop.SafeReleaseReadiness(now.Add(3 * time.Minute)).Healthy {
		t.Fatal("stale ticks reported healthy")
	}
}
