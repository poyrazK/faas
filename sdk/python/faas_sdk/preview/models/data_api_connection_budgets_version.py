from typing import Literal

DataAPIConnectionBudgetsVersion = Literal[1]

DATA_API_CONNECTION_BUDGETS_VERSION_VALUES: set[DataAPIConnectionBudgetsVersion] = {
    1,
}


def check_data_api_connection_budgets_version(value: int) -> DataAPIConnectionBudgetsVersion:
    if value in DATA_API_CONNECTION_BUDGETS_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_API_CONNECTION_BUDGETS_VERSION_VALUES!r}")
