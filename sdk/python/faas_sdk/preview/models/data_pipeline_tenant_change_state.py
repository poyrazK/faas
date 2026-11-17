from typing import Literal

DataPipelineTenantChangeState = Literal["applied", "approved", "preview"]

DATA_PIPELINE_TENANT_CHANGE_STATE_VALUES: set[DataPipelineTenantChangeState] = {
    "applied",
    "approved",
    "preview",
}


def check_data_pipeline_tenant_change_state(value: str) -> DataPipelineTenantChangeState:
    if value in DATA_PIPELINE_TENANT_CHANGE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_TENANT_CHANGE_STATE_VALUES!r}")
