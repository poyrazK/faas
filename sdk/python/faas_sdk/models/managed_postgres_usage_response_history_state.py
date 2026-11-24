from typing import Literal

ManagedPostgresUsageResponseHistoryState = Literal["limited", "normal"]

MANAGED_POSTGRES_USAGE_RESPONSE_HISTORY_STATE_VALUES: set[ManagedPostgresUsageResponseHistoryState] = {
    "limited",
    "normal",
}


def check_managed_postgres_usage_response_history_state(value: str) -> ManagedPostgresUsageResponseHistoryState:
    if value in MANAGED_POSTGRES_USAGE_RESPONSE_HISTORY_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_USAGE_RESPONSE_HISTORY_STATE_VALUES!r}"
    )
