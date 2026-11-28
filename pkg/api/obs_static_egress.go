package api

import "time"

// ObsStaticEgressAddress is one operator-provisioned static egress address
// (ADR-20261128090000): the provider routes IP to Node, and AccountID is the
// tenant it is assigned to (empty while unassigned). PinnedAppID names the
// live app currently pinning it.
type ObsStaticEgressAddress struct {
	IP          string    `json:"ip"`
	NodeID      string    `json:"node_id"`
	Node        string    `json:"node"`
	AccountID   string    `json:"account_id,omitempty"`
	Note        string    `json:"note,omitempty"`
	PinnedAppID string    `json:"pinned_app_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ObsStaticEgressAddressList is the operator inventory.
type ObsStaticEgressAddressList struct {
	Addresses []ObsStaticEgressAddress `json:"addresses"`
}

// ObsPutStaticEgressAddressRequest adds an address or replaces its node,
// account and note. Node is a compute node name; an empty AccountID leaves
// the address unassigned.
type ObsPutStaticEgressAddressRequest struct {
	Node      string `json:"node"`
	AccountID string `json:"account_id,omitempty"`
	Note      string `json:"note,omitempty"`
}
