package livequery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"

	"github.com/onebox-faas/faas/pkg/api"
)

// MarkerPrefix names the non-transactional logical messages that close a
// round. A marker is emitted after the round's snapshot is taken, so every
// transaction visible in that snapshot is decoded before the marker arrives.
const MarkerPrefix = "gregale_live_round"

const statusInterval = time.Second

// Tuple maps column names to text values. A nil value is SQL NULL; a missing
// column is an unchanged TOASTed value that pgoutput did not send.
type Tuple map[string]*string

// Change is one row change from a committed transaction.
type Change struct {
	Relation uint32 // table OID
	Op       byte   // 'I', 'U' or 'D'
	Old      Tuple  // replica-identity key or full old row; nil when absent
	New      Tuple  // nil for deletes
}

// Txn is a committed transaction as decoded from the slot.
type Txn struct {
	Xid        uint32
	EndLSN     pglogrepl.LSN
	CommitTime time.Time
	Changes    []Change
	Truncated  []uint32
}

// Event is either a committed transaction or a round marker.
type Event struct {
	Txn    *Txn
	Marker string
	LSN    pglogrepl.LSN
}

// Capture streams committed transactions from a pgoutput logical slot.
type Capture struct {
	// Created reports that the slot did not exist (or was lost) and was
	// created by this capture: nothing before it can be decoded, so a
	// restored engine must re-read every query.
	Created bool

	conn      *pgconn.PgConn
	events    chan Event
	confirmed atomic.Uint64
	cancel    context.CancelFunc
	done      chan struct{}

	mu  sync.Mutex
	err error
}

// OpenCapture creates a temporary pgoutput slot for the publication and starts
// streaming. dsn is an ordinary connection string; the replication parameter
// is added here.
func OpenCapture(ctx context.Context, dsn, slot, publication string) (*Capture, error) {
	return openCapture(ctx, dsn, slot, publication, false)
}

// OpenSlot streams from a persistent slot, creating it when it does not
// exist or was invalidated (wal_status lost). Streaming resumes after the
// slot's confirmed position, so transactions confirmed before a restart are
// not decoded again; those committed after it are, applied or not.
func OpenSlot(ctx context.Context, dsn, slot, publication string) (*Capture, error) {
	return openCapture(ctx, dsn, slot, publication, true)
}

func openCapture(ctx context.Context, dsn, slot, publication string, persistent bool) (*Capture, error) {
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("livequery: parse capture dsn: %w", err)
	}
	cfg.RuntimeParams["replication"] = "database"
	for k, v := range CaptureRuntimeParams {
		cfg.RuntimeParams[k] = v
	}
	conn, err := pgconn.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("livequery: connect capture: %w", err)
	}
	conn.Frontend().SetMaxBodyLen(api.LiveQueryReplicationMessageMaxBytes)
	create := true
	if persistent {
		if create, err = prepareSlot(ctx, conn, slot); err != nil {
			_ = conn.Close(ctx)
			return nil, err
		}
	}
	if create {
		if _, err := pglogrepl.CreateReplicationSlot(ctx, conn, slot, "pgoutput",
			pglogrepl.CreateReplicationSlotOptions{Temporary: !persistent, SnapshotAction: "NOEXPORT_SNAPSHOT"}); err != nil {
			_ = conn.Close(ctx)
			return nil, fmt.Errorf("livequery: create slot %s: %w", slot, err)
		}
	}
	args := []string{"proto_version '1'", fmt.Sprintf("publication_names '%s'", publication), "messages 'true'"}
	for {
		err := pglogrepl.StartReplication(ctx, conn, slot, 0, pglogrepl.StartReplicationOptions{PluginArgs: args})
		if err == nil {
			break
		}
		// A previous capture's walsender may still hold the slot for a
		// moment after its connection closed.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55006" && ctx.Err() == nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		_ = conn.Close(ctx)
		return nil, fmt.Errorf("livequery: start replication on %s: %w", slot, err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	c := &Capture{Created: create, conn: conn, events: make(chan Event, 1024), cancel: cancel, done: make(chan struct{})}
	go c.run(runCtx) //nolint:contextcheck // the Capture owns the stream's lifecycle; Close cancels it.
	return c, nil
}

// prepareSlot reports whether a persistent slot must be created, dropping
// one whose WAL is gone: it can no longer stream.
func prepareSlot(ctx context.Context, conn *pgconn.PgConn, slot string) (bool, error) {
	literal := "'" + strings.ReplaceAll(slot, "'", "''") + "'"
	results, err := conn.Exec(ctx, "SELECT coalesce(wal_status, '') FROM pg_replication_slots WHERE slot_name = "+literal).ReadAll()
	if err != nil {
		return false, fmt.Errorf("livequery: look up slot %s: %w", slot, err)
	}
	if len(results) == 0 || len(results[0].Rows) == 0 {
		return true, nil
	}
	if string(results[0].Rows[0][0]) != "lost" {
		return false, nil
	}
	if err := pglogrepl.DropReplicationSlot(ctx, conn, slot, pglogrepl.DropReplicationSlotOptions{}); err != nil {
		return false, fmt.Errorf("livequery: drop lost slot %s: %w", slot, err)
	}
	return true, nil
}

// Events delivers decoded transactions and markers in commit order. The
// channel closes when the capture stops; Err then reports why.
func (c *Capture) Events() <-chan Event { return c.events }

// Confirm records that everything before lsn has been applied, so the slot
// may release it.
func (c *Capture) Confirm(lsn pglogrepl.LSN) {
	for {
		current := c.confirmed.Load()
		if uint64(lsn) <= current || c.confirmed.CompareAndSwap(current, uint64(lsn)) {
			return
		}
	}
}

// Err reports the error that stopped the capture, if any.
func (c *Capture) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close stops streaming. A temporary slot goes with the connection; a
// persistent one keeps its confirmed position for the next OpenSlot.
func (c *Capture) Close() error {
	c.cancel()
	<-c.done
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.conn.Close(ctx)
}

func (c *Capture) fail(err error) {
	c.mu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.mu.Unlock()
}

func (c *Capture) run(ctx context.Context) {
	defer close(c.done)
	defer close(c.events)
	d := decoder{relations: map[uint32]*pglogrepl.RelationMessage{}}
	nextStatus := time.Now().Add(statusInterval)
	for {
		if time.Now().After(nextStatus) {
			if err := c.sendStatus(ctx); err != nil {
				c.fail(err)
				return
			}
			nextStatus = time.Now().Add(statusInterval)
		}
		recvCtx, cancel := context.WithDeadline(ctx, nextStatus)
		raw, err := c.conn.ReceiveMessage(recvCtx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if pgconn.Timeout(err) {
				continue
			}
			c.fail(fmt.Errorf("livequery: receive replication message: %w", err))
			return
		}
		switch msg := raw.(type) {
		case *pgproto3.ErrorResponse:
			c.fail(fmt.Errorf("livequery: replication error: %s", msg.Message))
			return
		case *pgproto3.CopyData:
			if len(msg.Data) == 0 {
				continue
			}
			switch msg.Data[0] {
			case pglogrepl.PrimaryKeepaliveMessageByteID:
				keepalive, err := pglogrepl.ParsePrimaryKeepaliveMessage(msg.Data[1:])
				if err != nil {
					c.fail(fmt.Errorf("livequery: parse keepalive: %w", err))
					return
				}
				if keepalive.ReplyRequested {
					nextStatus = time.Time{}
				}
			case pglogrepl.XLogDataByteID:
				xld, err := pglogrepl.ParseXLogData(msg.Data[1:])
				if err != nil {
					c.fail(fmt.Errorf("livequery: parse xlogdata: %w", err))
					return
				}
				event, ok, err := d.decode(xld)
				if err != nil {
					c.fail(err)
					return
				}
				if ok {
					select {
					case c.events <- event:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}
}

func (c *Capture) sendStatus(ctx context.Context) error {
	lsn := pglogrepl.LSN(c.confirmed.Load())
	err := pglogrepl.SendStandbyStatusUpdate(ctx, c.conn, pglogrepl.StandbyStatusUpdate{
		WALWritePosition: lsn, WALFlushPosition: lsn, WALApplyPosition: lsn,
	})
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("livequery: send standby status: %w", err)
	}
	return nil
}

// decoder assembles pgoutput messages into transactions.
type decoder struct {
	relations map[uint32]*pglogrepl.RelationMessage
	txn       *Txn
}

var errNoTxn = errors.New("livequery: row change outside a transaction")

func (d *decoder) decode(xld pglogrepl.XLogData) (Event, bool, error) {
	msg, err := pglogrepl.Parse(xld.WALData)
	if err != nil {
		return Event{}, false, fmt.Errorf("livequery: parse logical message: %w", err)
	}
	switch m := msg.(type) {
	case *pglogrepl.RelationMessage:
		d.relations[m.RelationID] = m
	case *pglogrepl.BeginMessage:
		d.txn = &Txn{Xid: m.Xid}
	case *pglogrepl.InsertMessage:
		return Event{}, false, d.add(m.RelationID, 'I', nil, m.Tuple)
	case *pglogrepl.UpdateMessage:
		return Event{}, false, d.add(m.RelationID, 'U', m.OldTuple, m.NewTuple)
	case *pglogrepl.DeleteMessage:
		return Event{}, false, d.add(m.RelationID, 'D', m.OldTuple, nil)
	case *pglogrepl.TruncateMessage:
		if d.txn == nil {
			return Event{}, false, errNoTxn
		}
		d.txn.Truncated = append(d.txn.Truncated, m.RelationIDs...)
	case *pglogrepl.CommitMessage:
		if d.txn == nil {
			return Event{}, false, errors.New("livequery: commit without begin")
		}
		txn := d.txn
		d.txn = nil
		txn.EndLSN = m.TransactionEndLSN
		txn.CommitTime = m.CommitTime
		return Event{Txn: txn, LSN: m.TransactionEndLSN}, true, nil
	case *pglogrepl.LogicalDecodingMessage:
		if !m.Transactional && m.Prefix == MarkerPrefix {
			return Event{Marker: string(m.Content), LSN: m.LSN}, true, nil
		}
	}
	return Event{}, false, nil
}

func (d *decoder) add(relation uint32, op byte, oldData, newData *pglogrepl.TupleData) error {
	if d.txn == nil {
		return errNoTxn
	}
	rel, ok := d.relations[relation]
	if !ok {
		return fmt.Errorf("livequery: change for unknown relation %d", relation)
	}
	d.txn.Changes = append(d.txn.Changes, Change{
		Relation: relation, Op: op, Old: tupleOf(rel, oldData), New: tupleOf(rel, newData),
	})
	return nil
}

func tupleOf(rel *pglogrepl.RelationMessage, data *pglogrepl.TupleData) Tuple {
	if data == nil {
		return nil
	}
	tuple := make(Tuple, len(data.Columns))
	for i, col := range data.Columns {
		if i >= len(rel.Columns) {
			break
		}
		name := rel.Columns[i].Name
		switch col.DataType {
		case pglogrepl.TupleDataTypeNull:
			tuple[name] = nil
		case pglogrepl.TupleDataTypeText:
			value := string(col.Data)
			tuple[name] = &value
		}
	}
	return tuple
}
