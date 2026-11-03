from typing import Literal

CreateDataAPIContractTaskRequestKind = Literal["schema", "types", "validators"]

CREATE_DATA_API_CONTRACT_TASK_REQUEST_KIND_VALUES: set[CreateDataAPIContractTaskRequestKind] = {
    "schema",
    "types",
    "validators",
}


def check_create_data_api_contract_task_request_kind(value: str) -> CreateDataAPIContractTaskRequestKind:
    if value in CREATE_DATA_API_CONTRACT_TASK_REQUEST_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_DATA_API_CONTRACT_TASK_REQUEST_KIND_VALUES!r}"
    )
