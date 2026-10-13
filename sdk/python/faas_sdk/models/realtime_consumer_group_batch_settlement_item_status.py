from typing import Literal

RealtimeConsumerGroupBatchSettlementItemStatus = Literal["completed", "dead", "lease_lost", "leased", "ready"]

REALTIME_CONSUMER_GROUP_BATCH_SETTLEMENT_ITEM_STATUS_VALUES: set[RealtimeConsumerGroupBatchSettlementItemStatus] = {
    "completed",
    "dead",
    "lease_lost",
    "leased",
    "ready",
}


def check_realtime_consumer_group_batch_settlement_item_status(
    value: str,
) -> RealtimeConsumerGroupBatchSettlementItemStatus:
    if value in REALTIME_CONSUMER_GROUP_BATCH_SETTLEMENT_ITEM_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_CONSUMER_GROUP_BATCH_SETTLEMENT_ITEM_STATUS_VALUES!r}"
    )
