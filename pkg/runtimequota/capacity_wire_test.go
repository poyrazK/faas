package runtimequota

// adr: 631
import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCapacityWireCanonicalBounds(t *testing.T) {
	g, directory, cell, trust := fixture(t)
	p := capacityPermitFixture(g)
	stop := CapacityStop{Permit: p, FenceID: uuid.NewString(), AdmissionFrozen: true}
	for _, tc := range []struct {
		name   string
		value  any
		decode func([]byte) error
	}{
		{"permit", p, func(raw []byte) error {
			got, err := DecodeCapacityPermit(raw)
			if err == nil && got != p {
				t.Fatal("permit changed")
			}
			return err
		}},
		{"stop", stop, func(raw []byte) error {
			got, err := DecodeCapacityStop(raw)
			if err == nil && got != stop {
				t.Fatal("stop changed")
			}
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.value)
			if err != nil || tc.decode(raw) != nil {
				t.Fatal("canonical object refused", err)
			}
			for _, bad := range [][]byte{
				nil, []byte("null"), []byte("{}"), append([]byte(" "), raw...), append(append([]byte(nil), raw...), raw...),
				[]byte(strings.Replace(string(raw), "{", `{"Unknown":1,`, 1)),
				[]byte(strings.Replace(string(raw), `"Grant":`, `"Grant":{},"Grant":`, 1)),
				make([]byte, api.RegionalRuntimeHostRecordMaxBytes+1),
			} {
				if tc.decode(bad) == nil {
					t.Fatalf("ambiguous object accepted: %.100q", bad)
				}
			}
		})
	}
	if trust.CheckSigner(cell, Cell) != nil || trust.CheckSigner(directory, Directory) != nil {
		t.Fatal("configured signer refused")
	}
	if trust.CheckSigner(cell, Directory) == nil || trust.CheckSigner(directory, Cell) == nil || trust.CheckSigner(Signer{}, Cell) == nil {
		t.Fatal("wrong signer role accepted")
	}
}
