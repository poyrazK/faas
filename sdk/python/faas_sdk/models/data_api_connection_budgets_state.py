from typing import Literal

DataAPIConnectionBudgetsState = Literal["not_applicable", "over_budget", "unknown", "within_budget"]

DATA_API_CONNECTION_BUDGETS_STATE_VALUES: set[DataAPIConnectionBudgetsState] = {
    "not_applicable",
    "over_budget",
    "unknown",
    "within_budget",
}


def check_data_api_connection_budgets_state(value: str) -> DataAPIConnectionBudgetsState:
    if value in DATA_API_CONNECTION_BUDGETS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_API_CONNECTION_BUDGETS_STATE_VALUES!r}")
