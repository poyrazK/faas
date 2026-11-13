package fcvm

// Host side of the stateless-runtime advisory wire (ADR-047, amended
// 2026-11-12). guest-init opens one guest-initiated vsock STREAM per batch to
// host port VsockStatelessAdvisoryHostPort; Firecracker forwards it to
// <vsock.sock>_1025, where vmmd's receiver decodes it with
// DecodeStatelessAdvisoryFrame. The guest is untrusted: everything here is
// bounded and validated, and the receiver takes the app from the listener's
// origin, never from the frame.

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"
)

const (
	// VsockStatelessAdvisoryHostPort is the guest-initiated port guest-init
	// ships advisory batches to (guest/init VsockStatelessAdvisoryPort).
	VsockStatelessAdvisoryHostPort uint32 = 1025
	// StatelessAdvisoryMsgType is the frame's leading big-endian uint32.
	StatelessAdvisoryMsgType uint32 = 2
	// StatelessAdvisoryMaxBody bounds one frame's JSON body (guest
	// advisoryMaxBody).
	StatelessAdvisoryMaxBody = 8 * 1024
	// StatelessAdvisoryMaxEvents bounds one batch (guest advisoryBatchCap).
	StatelessAdvisoryMaxEvents = 64
	// statelessAdvisoryMaxPath bounds one reported path.
	statelessAdvisoryMaxPath = 1024
	// statelessAdvisoryMaxMasks bounds one event's verb list.
	statelessAdvisoryMaxMasks = 4
)

// statelessAdvisoryVerbs is the closed verb vocabulary guest-init reports.
var statelessAdvisoryVerbs = map[string]bool{"create": true, "modify": true, "move": true, "other": true}

// ErrStatelessAdvisoryFrame marks a frame the receiver refuses.
var ErrStatelessAdvisoryFrame = errors.New("stateless advisory frame invalid")

// DecodeStatelessAdvisoryFrame reads one frame: msg_type (uint32 BE), body
// length (uint32 BE), then a JSON body {"events":[{path, mask, ts_unix_ms}]}.
// It reads exactly the framed bytes and never waits for EOF, because
// Firecracker's vsock does not reliably pass a half-close through.
func DecodeStatelessAdvisoryFrame(r io.Reader) ([]AdvisoryEvent, error) {
	var header [8]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, fmt.Errorf("%w: read header: %w", ErrStatelessAdvisoryFrame, err)
	}
	if msgType := binary.BigEndian.Uint32(header[0:4]); msgType != StatelessAdvisoryMsgType {
		return nil, fmt.Errorf("%w: message type %d", ErrStatelessAdvisoryFrame, msgType)
	}
	size := binary.BigEndian.Uint32(header[4:8])
	if size == 0 || size > StatelessAdvisoryMaxBody {
		return nil, fmt.Errorf("%w: body length %d outside 1..%d", ErrStatelessAdvisoryFrame, size, StatelessAdvisoryMaxBody)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("%w: read body: %w", ErrStatelessAdvisoryFrame, err)
	}
	var batch struct {
		Events []struct {
			Path     string   `json:"path"`
			Mask     []string `json:"mask"`
			TsUnixMs int64    `json:"ts_unix_ms"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &batch); err != nil {
		return nil, fmt.Errorf("%w: decode body: %w", ErrStatelessAdvisoryFrame, err)
	}
	if len(batch.Events) > StatelessAdvisoryMaxEvents {
		return nil, fmt.Errorf("%w: %d events, limit %d", ErrStatelessAdvisoryFrame, len(batch.Events), StatelessAdvisoryMaxEvents)
	}
	out := make([]AdvisoryEvent, 0, len(batch.Events))
	for _, ev := range batch.Events {
		if err := validStatelessAdvisoryEvent(ev.Path, ev.Mask); err != nil {
			return nil, err
		}
		out = append(out, AdvisoryEvent{Path: ev.Path, Masks: ev.Mask, TsUnix: ev.TsUnixMs})
	}
	return out, nil
}

func validStatelessAdvisoryEvent(p string, masks []string) error {
	if p == "" || len(p) > statelessAdvisoryMaxPath || !utf8.ValidString(p) ||
		strings.ContainsRune(p, 0) || !path.IsAbs(p) || path.Clean(p) != p {
		return fmt.Errorf("%w: path %.80q", ErrStatelessAdvisoryFrame, p)
	}
	if len(masks) == 0 || len(masks) > statelessAdvisoryMaxMasks {
		return fmt.Errorf("%w: %d verbs for %.80q", ErrStatelessAdvisoryFrame, len(masks), p)
	}
	for _, m := range masks {
		if !statelessAdvisoryVerbs[m] {
			return fmt.Errorf("%w: verb %.20q", ErrStatelessAdvisoryFrame, m)
		}
	}
	return nil
}
