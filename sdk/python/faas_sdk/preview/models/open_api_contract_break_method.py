from typing import Literal

OpenAPIContractBreakMethod = Literal["delete", "get", "head", "options", "patch", "post", "put", "trace"]

OPEN_API_CONTRACT_BREAK_METHOD_VALUES: set[OpenAPIContractBreakMethod] = {
    "delete",
    "get",
    "head",
    "options",
    "patch",
    "post",
    "put",
    "trace",
}


def check_open_api_contract_break_method(value: str) -> OpenAPIContractBreakMethod:
    if value in OPEN_API_CONTRACT_BREAK_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPEN_API_CONTRACT_BREAK_METHOD_VALUES!r}")
