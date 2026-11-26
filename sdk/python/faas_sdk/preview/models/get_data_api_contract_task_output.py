from typing import Literal

GetDataAPIContractTaskOutput = Literal["artifact"]

GET_DATA_API_CONTRACT_TASK_OUTPUT_VALUES: set[GetDataAPIContractTaskOutput] = {
    "artifact",
}


def check_get_data_api_contract_task_output(value: str) -> GetDataAPIContractTaskOutput:
    if value in GET_DATA_API_CONTRACT_TASK_OUTPUT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_DATA_API_CONTRACT_TASK_OUTPUT_VALUES!r}")
