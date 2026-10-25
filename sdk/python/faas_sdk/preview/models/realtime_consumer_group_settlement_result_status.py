from typing import Literal

RealtimeConsumerGroupSettlementResultStatus = Literal["completed", "dead", "leased", "ready"]

REALTIME_CONSUMER_GROUP_SETTLEMENT_RESULT_STATUS_VALUES: set[RealtimeConsumerGroupSettlementResultStatus] = {
    "completed",
    "dead",
    "leased",
    "ready",
}


def check_realtime_consumer_group_settlement_result_status(value: str) -> RealtimeConsumerGroupSettlementResultStatus:
    if value in REALTIME_CONSUMER_GROUP_SETTLEMENT_RESULT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_CONSUMER_GROUP_SETTLEMENT_RESULT_STATUS_VALUES!r}"
    )
