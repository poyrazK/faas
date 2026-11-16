package state

import (
	"context"
	"math"
	"time"
)

// InternalEgressObserver records a network observation whose billed egress
// leaves out the bytes the instance sent to internal destinations (billing
// review R8-2, ADR-20261114100000). PgStore and MemStore implement it; meterd
// calls it instead of AppendNetworkUsageObservation only when [meter]
// internal_egress_unbilled is on and vmmd reported the internal counter.
type InternalEgressObserver interface {
	// AppendNetworkUsageObservationExcludingInternal is
	// AppendNetworkUsageObservation that also takes vmmd's cumulative
	// internal-egress reading. The internal bytes since the checkpoint (a
	// reading below it is a counter reset: all of it is new), plus any
	// service reply bytes the gateway queued, come off the egress this
	// observation adds, never below zero; what it cannot absorb carries to
	// the next observation, at most what arrived since the last one. Financial
	// usage evidence is append-only, so billed egress is reduced before it is
	// written. It returns the billed egress delta, the ingress delta and the
	// bytes excluded; the checkpoint advances in the same transaction.
	AppendNetworkUsageObservationExcludingInternal(ctx context.Context, accountID, appID, instanceID string, minute time.Time, netTxCumulative int64, netTxValid bool, netRxCumulative int64, netRxValid bool, internalCumulative int64) (netTxDelta, netRxDelta, excluded int64, err error)
}

// internalEgressStep is the checkpoint arithmetic both stores share: the new
// bytes since prev plus the service reply bytes the gateway queued since the
// last call (fresh), the bytes to subtract from a row holding rowNetTx, and
// the carry left for the next reading.
func internalEgressStep(prev int64, prevValid bool, pending, fresh, cumulative, rowNetTx int64) (subtract, nextPending int64) {
	delta := cumulative
	if prevValid && cumulative >= prev {
		delta = cumulative - prev
	}
	arrived := saturatingAdd(delta, max(fresh, 0))
	want := saturatingAdd(arrived, pending)
	subtract = min(want, max(rowNetTx, 0))
	// A carry larger than what arrived since the last call would mean the
	// internal counts ran ahead of the veth counter for longer than one
	// poll; drop the excess rather than let it erase later public egress.
	return subtract, min(want-subtract, arrived)
}

func saturatingAdd(a, b int64) int64 {
	if b > math.MaxInt64-a {
		return math.MaxInt64
	}
	return a + b
}

const (
	internalEgressCheckpointUpdate = `update meter_network_checkpoints
	    set internal_tx_bytes = $2,
	        internal_tx_valid = true,
	        internal_tx_pending = $3,
	        service_reply_pending = 0,
	        updated_at = now()
	  where instance_id = $1`
	// serviceReplyQueue adds a gateway frame's service reply bytes to the
	// next subtraction, only once the node reports internal egress.
	serviceReplyQueue = `update meter_network_checkpoints
	    set service_reply_pending = case
	          when service_reply_pending > 9223372036854775807 - $2 then 9223372036854775807
	          else service_reply_pending + $2 end,
	        updated_at = now()
	  where instance_id = $1 and internal_tx_valid`
)
