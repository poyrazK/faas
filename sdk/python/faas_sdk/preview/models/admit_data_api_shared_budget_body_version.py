from typing import Literal

AdmitDataAPISharedBudgetBodyVersion = Literal[1]

ADMIT_DATA_API_SHARED_BUDGET_BODY_VERSION_VALUES: set[AdmitDataAPISharedBudgetBodyVersion] = {
    1,
}


def check_admit_data_api_shared_budget_body_version(value: int) -> AdmitDataAPISharedBudgetBodyVersion:
    if value in ADMIT_DATA_API_SHARED_BUDGET_BODY_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ADMIT_DATA_API_SHARED_BUDGET_BODY_VERSION_VALUES!r}")
