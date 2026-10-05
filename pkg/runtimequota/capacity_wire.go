package runtimequota

// adr: 631
import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

// These bounded canonical objects carry host bookkeeping and unsigned native
// assertions. Authentication belongs to the transport; cell signatures follow
// native verification and the cell's exact retained receipt checks.
func DecodeCapacityPermit(raw []byte) (CapacityPermit, error) {
	var p CapacityPermit
	if decodeCapacityObject(raw, &p) != nil || p.Validate() != nil {
		return CapacityPermit{}, ErrInvalid
	}
	return p, nil
}

func DecodeCapacityStop(raw []byte) (CapacityStop, error) {
	var s CapacityStop
	if decodeCapacityObject(raw, &s) != nil || s.Validate() != nil {
		return CapacityStop{}, ErrInvalid
	}
	return s, nil
}

func decodeCapacityObject(raw []byte, target any) error {
	return decodeCanonicalObject(raw, target, api.RegionalRuntimeHostRecordMaxBytes)
}

func decodeCanonicalObject(raw []byte, target any, bound int) error {
	if len(raw) > bound {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil || d.Decode(&struct{}{}) != io.EOF {
		return ErrInvalid
	}
	canonical, err := json.Marshal(target)
	if err != nil || !bytes.Equal(raw, canonical) {
		return ErrInvalid
	}
	return nil
}
