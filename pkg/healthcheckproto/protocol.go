// Package healthcheckproto defines fresh, host-initiated image readiness and
// recurring runtime checks.
// adr:643
package healthcheckproto

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

const (
	Port        uint32 = 1028 // Share the existing guest liveness listener.
	Probe       uint32 = 12
	Ack         uint32 = 13
	CheckOnce   uint32 = 14 // One recurring command attempt, without startup retries.
	CheckAck    uint32 = 15
	ConfigProbe uint32 = 16
	ConfigAck   uint32 = 17
	MaxBody            = 4096
	// Millisecond budgets must fit in a signed nanosecond duration.
	MaxProbeBudgetMS int64 = (1<<63 - 1) / 1000000
)

type Request struct {
	Nonce     string `json:"nonce"`
	BudgetMS  int64  `json:"budget_ms"`
	RuntimeID string `json:"runtime_id,omitempty"`
}

type Response struct {
	Nonce          string `json:"nonce"`
	Healthy        bool   `json:"healthy"`
	Error          string `json:"error,omitempty"`
	RuntimeID      string `json:"runtime_id,omitempty"`
	NextIntervalNS int64  `json:"next_interval_ns,omitempty"`
}

// Config carries effective immutable timings and the current main-process
// incarnation. It contains no command arguments, output, or environment.
type Config struct {
	Nonce      string `json:"nonce"`
	RuntimeID  string `json:"runtime_id"`
	Error      string `json:"error,omitempty"`
	IntervalNS int64  `json:"interval_ns"`
	TimeoutNS  int64  `json:"timeout_ns"`
	Retries    int    `json:"retries"`
}

func (c Config) Validate() error {
	if c.Error != "" || c.RuntimeID == "" || len(c.RuntimeID) > 128 || c.IntervalNS < 1000000 || c.TimeoutNS < 1000000 || c.Retries <= 0 {
		return fmt.Errorf("invalid image healthcheck configuration")
	}
	return nil
}

func (r Request) Validate(maxBudgetMS int64) error {
	if nonce, err := hex.DecodeString(r.Nonce); err != nil || len(nonce) != 32 {
		return fmt.Errorf("invalid healthcheck challenge")
	}
	if r.BudgetMS <= 0 || r.BudgetMS > maxBudgetMS {
		return fmt.Errorf("invalid healthcheck budget")
	}
	return nil
}

func Write(w io.Writer, kind uint32, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(body) > MaxBody {
		return fmt.Errorf("healthcheck frame exceeds limit")
	}
	var header [8]byte
	binary.BigEndian.PutUint32(header[:4], kind)
	binary.BigEndian.PutUint32(header[4:], uint32(len(body)))
	_, err = io.Copy(w, io.MultiReader(bytes.NewReader(header[:]), bytes.NewReader(body)))
	return err
}

func Read(r io.Reader, kind uint32, value any) error {
	var header [8]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	if binary.BigEndian.Uint32(header[:4]) != kind {
		return fmt.Errorf("unexpected healthcheck message")
	}
	return ReadBody(r, binary.BigEndian.Uint32(header[4:]), value)
}

func ReadBody(r io.Reader, size uint32, value any) error {
	if size == 0 || size > MaxBody {
		return fmt.Errorf("invalid healthcheck frame length")
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return err
	}
	return json.Unmarshal(body, value)
}
