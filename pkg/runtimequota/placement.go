package runtimequota

// adr: 619
import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

// Placement carries immutable host bookkeeping. It is not an execution grant;
// the receiving host must separately check the current cell authority.
type Placement struct {
	Permit CapacityPermit
	Seat   *ServingSeat
}

func (p Placement) Validate() error {
	if p.Permit.Validate() != nil || (p.Seat != nil && (p.Seat.Validate() != nil || p.Seat.Permit != p.Permit)) {
		return ErrInvalid
	}
	return nil
}

func (p Placement) Clone() Placement {
	if p.Seat != nil {
		s := *p.Seat
		p.Seat = &s
	}
	return p
}

// DecodePlacement rejects ambiguous or oversized internal transport objects.
func DecodePlacement(raw []byte) (Placement, error) {
	var p Placement
	if len(raw) > api.RegionalRuntimeHostRecordMaxBytes {
		return p, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(&struct{}{}) != io.EOF || p.Validate() != nil {
		return Placement{}, ErrInvalid
	}
	canonical, err := json.Marshal(p)
	if err != nil || !bytes.Equal(canonical, raw) {
		return Placement{}, ErrInvalid
	}
	return p, nil
}
