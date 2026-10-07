package fcvm

// adr: 649
import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

// NativeRuntimeInventorySample binds an advisory observation to one request and
// journal incarnation. Even a valid sample grants no launch, release or resume.
type NativeRuntimeInventorySample struct {
	Version   int
	Host      runtimequota.HostEpoch
	Challenge string
	Inventory NativeRuntimeInventory
}

func (s NativeRuntimeInventorySample) Validate() error {
	if s.Version != 1 || s.Host.Validate() != nil || !nativeInventoryUUID(s.Challenge) || !nativeInventoryUUID(s.Inventory.KernelBootID) {
		return runtimequota.ErrBinding
	}
	return nil
}

func nativeInventoryUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func EncodeNativeRuntimeInventorySample(sample NativeRuntimeInventorySample) ([]byte, error) {
	if err := sample.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(sample)
	if err != nil || len(raw) > api.NativeRuntimeInventoryMaxBytes {
		return nil, runtimequota.ErrInvalid
	}
	return raw, nil
}

// Canonical equality also rejects duplicate fields, null substitutions,
// alternate casing and encodings, even in nested original provenance.
func DecodeNativeRuntimeInventorySample(raw []byte) (NativeRuntimeInventorySample, error) {
	var sample NativeRuntimeInventorySample
	if len(raw) == 0 || len(raw) > api.NativeRuntimeInventoryMaxBytes {
		return sample, runtimequota.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&sample) != nil || d.Decode(&struct{}{}) != io.EOF || sample.Validate() != nil {
		return NativeRuntimeInventorySample{}, runtimequota.ErrInvalid
	}
	canonical, err := json.Marshal(sample)
	if err != nil || !bytes.Equal(raw, canonical) {
		return NativeRuntimeInventorySample{}, runtimequota.ErrInvalid
	}
	return sample, nil
}

// RegionalRuntimeCompatibility repeats the native comparison at the receiver.
// It compares observations only; current run/task authority is still separate.
func (o NativeRuntimeObservation) RegionalRuntimeCompatibility() string {
	if o.Machine == nil || o.OriginalResources == nil || !o.OriginalResources.Valid() {
		return "original resources or actual machine are unavailable"
	}
	return nativeRuntimeCompatibility(nativeLaunchRecord{Lease: o.Lease, PID: o.PID, RegionalResources: o.OriginalResources}, *o.Machine, o.Cgroup)
}
