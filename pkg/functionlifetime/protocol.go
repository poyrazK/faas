// Package functionlifetime carries acknowledged execution ownership between
// the function runner, guest-init and vmmd (ADR-1542). EOF is never completion.
package functionlifetime

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	Version              = 1
	HostPort      uint32 = 1033
	SocketPath           = "/run/guest-init/function-executions.sock"
	MaxFrameBytes        = 1024
	RPCTimeout           = 2 * time.Second
	Begin                = "begin"
	Finish               = "finish"
	Retire               = "retire"
	OK                   = "ok"
	Busy                 = "busy"
	Stale                = "stale"
	Unavailable          = "unavailable"
	Limit                = "limit"
)

// Owner is a guest-init process generation, never an application-provided PID
// or instance ID. The receiving listener supplies the physical VM identity.
type Request struct {
	Version  int    `json:"version"`
	Owner    uint64 `json:"owner"`
	Sequence uint64 `json:"sequence"`
	Kind     string `json:"kind"`
	BudgetMS int64  `json:"budget_ms,omitempty"`
	Hook     string `json:"hook,omitempty"`
}

type Reply struct {
	Version int      `json:"version"`
	Status  string   `json:"status"`
	Plan    api.Plan `json:"plan,omitempty"`
}

func (r Request) Validate() error {
	if r.Version != Version || (r.Hook != "" && r.Hook != "before_checkpoint") {
		return errors.New("function lifetime: invalid version or hook")
	}
	switch r.Kind {
	case Begin:
		if r.Sequence == 0 || r.BudgetMS <= 0 || r.BudgetMS > api.RequestBudgetPlanMax.Milliseconds() {
			return errors.New("function lifetime: invalid execution budget or sequence")
		}
	case Finish:
		if r.Sequence == 0 || r.BudgetMS != 0 || r.Hook != "" {
			return errors.New("function lifetime: invalid completion")
		}
	case Retire:
		if r.Sequence != 0 || r.BudgetMS != 0 || r.Hook != "" {
			return errors.New("function lifetime: invalid retirement")
		}
	default:
		return errors.New("function lifetime: unknown operation")
	}
	return nil
}

func ReadRequest(r io.Reader) (Request, error) {
	var req Request
	if err := read(r, &req); err != nil {
		return req, err
	}
	return req, req.Validate()
}

func ReadReply(r io.Reader) (Reply, error) {
	var reply Reply
	if err := read(r, &reply); err != nil {
		return reply, err
	}
	if reply.Version != Version {
		return reply, errors.New("function lifetime: invalid reply version")
	}
	switch reply.Status {
	case OK, Busy, Stale, Unavailable, Limit:
		return reply, nil
	default:
		return reply, errors.New("function lifetime: invalid reply status")
	}
}

func Write(w io.Writer, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(body) > MaxFrameBytes {
		return errors.New("function lifetime: oversized frame")
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(body)))
	if _, err := io.Copy(w, bytes.NewReader(size[:])); err != nil {
		return err
	}
	_, err = io.Copy(w, bytes.NewReader(body))
	return err
}

func read(r io.Reader, value any) error {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > MaxFrameBytes {
		return errors.New("function lifetime: invalid frame size")
	}
	body := make([]byte, int(n))
	if _, err := io.ReadFull(r, body); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return fmt.Errorf("function lifetime: decode: %w", err)
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("function lifetime: trailing frame data")
	}
	return nil
}
