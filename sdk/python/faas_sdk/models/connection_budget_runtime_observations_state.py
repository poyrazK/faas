from typing import Literal

ConnectionBudgetRuntimeObservationsState = Literal["not_applicable", "observed", "partial", "unknown"]

CONNECTION_BUDGET_RUNTIME_OBSERVATIONS_STATE_VALUES: set[ConnectionBudgetRuntimeObservationsState] = {
    "not_applicable",
    "observed",
    "partial",
    "unknown",
}


def check_connection_budget_runtime_observations_state(value: str) -> ConnectionBudgetRuntimeObservationsState:
    if value in CONNECTION_BUDGET_RUNTIME_OBSERVATIONS_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CONNECTION_BUDGET_RUNTIME_OBSERVATIONS_STATE_VALUES!r}"
    )
