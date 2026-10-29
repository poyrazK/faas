from typing import Literal

ConnectionBudgetRuntimeObservationsSource = Literal["workload_report"]

CONNECTION_BUDGET_RUNTIME_OBSERVATIONS_SOURCE_VALUES: set[ConnectionBudgetRuntimeObservationsSource] = {
    "workload_report",
}


def check_connection_budget_runtime_observations_source(value: str) -> ConnectionBudgetRuntimeObservationsSource:
    if value in CONNECTION_BUDGET_RUNTIME_OBSERVATIONS_SOURCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CONNECTION_BUDGET_RUNTIME_OBSERVATIONS_SOURCE_VALUES!r}"
    )
