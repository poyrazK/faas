package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/wire"
)

// The scheduler is the sole writer of instance readiness. The timestamp is
// chosen there; a replay cannot move the first-ready age floor.
type frameworkReadyReporter struct {
	target    string
	tlsConfig *tls.Config
}

func (r *frameworkReadyReporter) SetFrameworkReadyAt(ctx context.Context, id string, _ time.Time) error {
	if r.target == "" {
		return fmt.Errorf("framework-ready scheduler target is not configured")
	}
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := wire.DialContext(callCtx, r.target, r.tlsConfig)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_, err = scheddpb.NewScheddClient(conn).ReportFrameworkReady(callCtx, &scheddpb.FrameworkReadyReport{InstanceId: id})
	return err
}
