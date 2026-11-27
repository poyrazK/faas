from typing import Literal

ManagedPostgresQueryCancellationOutcome = Literal["signal_sent", "stale_target", "unconfirmed"]

MANAGED_POSTGRES_QUERY_CANCELLATION_OUTCOME_VALUES: set[ManagedPostgresQueryCancellationOutcome] = {
    "signal_sent",
    "stale_target",
    "unconfirmed",
}


def check_managed_postgres_query_cancellation_outcome(value: str) -> ManagedPostgresQueryCancellationOutcome:
    if value in MANAGED_POSTGRES_QUERY_CANCELLATION_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_QUERY_CANCELLATION_OUTCOME_VALUES!r}"
    )
