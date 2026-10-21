// Package promotionfault implements the private native publication barrier.
// Production builds compile Hit to a no-op; the protocol never supplies health,
// runtime, publication or checkpoint evidence to a daemon.
package promotionfault

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const SocketEnv = "FAAS_E2E_PROMOTION_PUBLICATION_SOCKET"

const (
	CutoverBeforeCommit  = "cutover_before_commit"
	CutoverAfterCommit   = "cutover_after_commit"
	RollbackBeforeCommit = "rollback_before_commit"
	RollbackAfterCommit  = "rollback_after_commit"
)

type Event struct {
	SchemaVersion int       `json:"schema_version"`
	ProjectID     string    `json:"project_id"`
	PromotionID   string    `json:"promotion_id"`
	Point         string    `json:"point"`
	ReleaseID     string    `json:"release_id"`
	PID           int       `json:"pid"`
	ObservedAt    time.Time `json:"observed_at"`
	Deadline      time.Time `json:"deadline"`
}

// Wait keeps the daemon at this boundary until the harness acknowledges it or
// kills the owned process. A missing harness cannot hold a worker indefinitely.
func Wait(ctx context.Context, conn net.Conn, event Event) error {
	defer func() { _ = conn.Close() }()
	event.ObservedAt = time.Now().UTC()
	deadline := event.ObservedAt.Add(api.ProjectPromotionHealthJobTimeout)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	event.Deadline = deadline
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := json.NewEncoder(conn).Encode(event); err != nil {
		return err
	}
	var ack struct {
		Continue bool `json:"continue"`
	}
	decoder := json.NewDecoder(io.LimitReader(conn, api.PromotionPublicationAckMaxBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ack); err != nil {
		return err
	}
	if !ack.Continue {
		return errors.New("publication barrier not acknowledged")
	}
	return nil
}
