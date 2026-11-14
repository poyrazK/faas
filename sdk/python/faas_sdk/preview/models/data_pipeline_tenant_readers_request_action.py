from typing import Literal

DataPipelineTenantReadersRequestAction = Literal["approve", "discard", "preview"]

DATA_PIPELINE_TENANT_READERS_REQUEST_ACTION_VALUES: set[DataPipelineTenantReadersRequestAction] = {
    "approve",
    "discard",
    "preview",
}


def check_data_pipeline_tenant_readers_request_action(value: str) -> DataPipelineTenantReadersRequestAction:
    if value in DATA_PIPELINE_TENANT_READERS_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_TENANT_READERS_REQUEST_ACTION_VALUES!r}"
    )
