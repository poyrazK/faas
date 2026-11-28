from typing import Literal

CreateDataAPIContractTaskOutput = Literal["artifact"]

CREATE_DATA_API_CONTRACT_TASK_OUTPUT_VALUES: set[CreateDataAPIContractTaskOutput] = {
    "artifact",
}


def check_create_data_api_contract_task_output(value: str) -> CreateDataAPIContractTaskOutput:
    if value in CREATE_DATA_API_CONTRACT_TASK_OUTPUT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_DATA_API_CONTRACT_TASK_OUTPUT_VALUES!r}")
