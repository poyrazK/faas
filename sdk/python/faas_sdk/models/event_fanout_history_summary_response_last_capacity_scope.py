from typing import Literal

EventFanoutHistorySummaryResponseLastCapacityScope = Literal["account", "app", "consumer"]

EVENT_FANOUT_HISTORY_SUMMARY_RESPONSE_LAST_CAPACITY_SCOPE_VALUES: set[
    EventFanoutHistorySummaryResponseLastCapacityScope
] = {
    "account",
    "app",
    "consumer",
}


def check_event_fanout_history_summary_response_last_capacity_scope(
    value: str,
) -> EventFanoutHistorySummaryResponseLastCapacityScope:
    if value in EVENT_FANOUT_HISTORY_SUMMARY_RESPONSE_LAST_CAPACITY_SCOPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_FANOUT_HISTORY_SUMMARY_RESPONSE_LAST_CAPACITY_SCOPE_VALUES!r}"
    )
