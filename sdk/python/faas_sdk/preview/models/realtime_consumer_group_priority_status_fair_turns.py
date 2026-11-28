from typing import Literal

RealtimeConsumerGroupPriorityStatusFairTurns = Literal[8]

REALTIME_CONSUMER_GROUP_PRIORITY_STATUS_FAIR_TURNS_VALUES: set[RealtimeConsumerGroupPriorityStatusFairTurns] = {
    8,
}


def check_realtime_consumer_group_priority_status_fair_turns(
    value: int,
) -> RealtimeConsumerGroupPriorityStatusFairTurns:
    if value in REALTIME_CONSUMER_GROUP_PRIORITY_STATUS_FAIR_TURNS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_CONSUMER_GROUP_PRIORITY_STATUS_FAIR_TURNS_VALUES!r}"
    )
