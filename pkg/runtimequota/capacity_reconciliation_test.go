package runtimequota

// adr: 642
import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCapacityStopFenceRelationAndLegacyEncoding(t *testing.T) {
	g, _, cell, trust := fixture(t)
	stop := CapacityStop{Permit: capacityPermitFixture(g), FenceID: uuid.NewString(), AdmissionFrozen: true}
	legacy := struct {
		Permit          CapacityPermit
		FenceID         string
		AdmissionFrozen bool
		CountedRuntimes int
	}{stop.Permit, stop.FenceID, stop.AdmissionFrozen, stop.CountedRuntimes}
	want, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(stop)
	if err != nil || !bytes.Equal(raw, want) {
		t.Fatal("legacy canonical encoding changed", err)
	}
	if got, err := DecodeCapacityStop(want); err != nil || got != stop {
		t.Fatal("legacy host assertion refused", err)
	}
	oldEnvelope, err := sign(cell, "capacity-stop", legacy)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := trust.VerifyCapacityStop(oldEnvelope); err != nil || got != stop {
		t.Fatal("legacy signed receipt refused", err)
	}
	stop.HostFenceID = "abcdefab-1234-4234-8234-abcdefabcdef"
	e, err := cell.SignCapacityStop(stop)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := trust.VerifyCapacityStop(e); err != nil || got != stop {
		t.Fatal("signed relation lost a first fence", err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(e.Payload)
	if err != nil {
		t.Fatal(err)
	}
	tampered := e
	tampered.Payload = base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(payload), stop.HostFenceID, uuid.NewString(), 1)))
	if _, err = trust.VerifyCapacityStop(tampered); err == nil {
		t.Fatal("host fence was not bound by the cell signature")
	}
	raw, err = json.Marshal(stop)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeCapacityStop(raw); err == nil {
		t.Fatal("native host supplied a cell fence relation")
	}
	for _, fence := range []string{stop.FenceID, uuid.Nil.String(), strings.ToUpper(stop.HostFenceID), "invalid"} {
		bad := stop
		bad.HostFenceID = fence
		if _, err = cell.SignCapacityStop(bad); err == nil {
			t.Fatal("invalid or redundant relation signed", fence)
		}
	}
}
