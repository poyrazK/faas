from typing import Literal

OpenAPIContractDiffResponseSource = Literal["edge_rules", "manual_import"]

OPEN_API_CONTRACT_DIFF_RESPONSE_SOURCE_VALUES: set[OpenAPIContractDiffResponseSource] = {
    "edge_rules",
    "manual_import",
}


def check_open_api_contract_diff_response_source(value: str) -> OpenAPIContractDiffResponseSource:
    if value in OPEN_API_CONTRACT_DIFF_RESPONSE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPEN_API_CONTRACT_DIFF_RESPONSE_SOURCE_VALUES!r}")
