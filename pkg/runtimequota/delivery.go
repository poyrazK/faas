package runtimequota

// adr: 772
import (
	"bytes"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
)

// Delivery repeats the expected binding outside the signed payload so the
// receiver can compare its own cell, origin and authenticated local owner.
type Delivery struct {
	Binding   Binding  `json:"binding"`
	Authority Envelope `json:"authority"`
	ConsentID string   `json:"consent_id,omitempty"` // ADR-773; omission preserves ADR-772 wire spelling.
}

// DeliveryReceipt carries retained directory evidence. It asserts receipt
// storage only, never admission activation, native absence or a quota drain.
type DeliveryReceipt struct {
	Grant      *Envelope `json:"grant"`
	Retirement *Envelope `json:"retirement"`
}

func DecodeDelivery(raw []byte) (Delivery, error) {
	var d Delivery
	if err := decodeCanonicalObject(raw, &d, api.RegionalRuntimeQuotaDeliveryMaxBytes); err != nil || d.Binding.Validate() != nil || d.ConsentID != "" && !validID(d.ConsentID) {
		return Delivery{}, ErrInvalid
	}
	e, err := json.Marshal(d.Authority)
	if err != nil {
		return Delivery{}, ErrInvalid
	}
	if _, err := Decode(e); err != nil {
		return Delivery{}, err
	}
	return d, nil
}

func DecodeDeliveryReceipt(raw []byte) (DeliveryReceipt, error) {
	var r DeliveryReceipt
	if err := decodeCanonicalObject(bytes.TrimSuffix(raw, []byte("\n")), &r, api.RegionalRuntimeQuotaDeliveryMaxBytes); err != nil || r.Grant == nil && r.Retirement == nil {
		return DeliveryReceipt{}, ErrInvalid
	}
	return r, nil
}
