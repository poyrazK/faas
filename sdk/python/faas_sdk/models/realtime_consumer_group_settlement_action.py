from typing import Literal

RealtimeConsumerGroupSettlementAction = Literal["complete", "release", "renew", "retry"]

REALTIME_CONSUMER_GROUP_SETTLEMENT_ACTION_VALUES: set[RealtimeConsumerGroupSettlementAction] = {
    "complete",
    "release",
    "renew",
    "retry",
}


def check_realtime_consumer_group_settlement_action(value: str) -> RealtimeConsumerGroupSettlementAction:
    if value in REALTIME_CONSUMER_GROUP_SETTLEMENT_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {REALTIME_CONSUMER_GROUP_SETTLEMENT_ACTION_VALUES!r}")
