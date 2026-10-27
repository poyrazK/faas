from typing import Literal

RealtimeConsumerGroupBatchSettlementItemAction = Literal["complete", "release", "renew", "retry"]

REALTIME_CONSUMER_GROUP_BATCH_SETTLEMENT_ITEM_ACTION_VALUES: set[RealtimeConsumerGroupBatchSettlementItemAction] = {
    "complete",
    "release",
    "renew",
    "retry",
}


def check_realtime_consumer_group_batch_settlement_item_action(
    value: str,
) -> RealtimeConsumerGroupBatchSettlementItemAction:
    if value in REALTIME_CONSUMER_GROUP_BATCH_SETTLEMENT_ITEM_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_CONSUMER_GROUP_BATCH_SETTLEMENT_ITEM_ACTION_VALUES!r}"
    )
