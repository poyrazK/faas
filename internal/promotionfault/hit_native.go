//go:build linux && amd64 && metal && promotionacceptance

package promotionfault

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/promotionrollout"
)

func Hit(ctx context.Context, project, promotion, point, release string) error {
	path := os.Getenv(SocketEnv)
	if path == "" {
		return nil
	}
	// Reuse the native admission guard: root/designated host/KVM and a private
	// local test catalog are required even in an explicitly tagged binary.
	if !filepath.IsAbs(path) || os.Getenv(promotionrollout.QualificationEnv) != "1" ||
		promotionrollout.FromEnv(os.Getenv, "unknown").Readiness(promotionrollout.Target{}).Mode != "qualification" {
		return errors.New("native publication barrier host invalid")
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return errors.New("native publication barrier unavailable")
	}
	if err := Wait(ctx, conn, Event{SchemaVersion: 1, ProjectID: project, PromotionID: promotion, Point: point, ReleaseID: release, PID: os.Getpid()}); err != nil {
		return errors.New("native publication barrier interrupted")
	}
	return nil
}
