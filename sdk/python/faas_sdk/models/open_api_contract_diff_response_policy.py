from typing import Literal

OpenAPIContractDiffResponsePolicy = Literal["block", "observe", "warn"]

OPEN_API_CONTRACT_DIFF_RESPONSE_POLICY_VALUES: set[OpenAPIContractDiffResponsePolicy] = {
    "block",
    "observe",
    "warn",
}


def check_open_api_contract_diff_response_policy(value: str) -> OpenAPIContractDiffResponsePolicy:
    if value in OPEN_API_CONTRACT_DIFF_RESPONSE_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPEN_API_CONTRACT_DIFF_RESPONSE_POLICY_VALUES!r}")
