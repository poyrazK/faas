from typing import Literal

AppResponseOpenapiContractPolicy = Literal["block", "observe", "warn"]

APP_RESPONSE_OPENAPI_CONTRACT_POLICY_VALUES: set[AppResponseOpenapiContractPolicy] = {
    "block",
    "observe",
    "warn",
}


def check_app_response_openapi_contract_policy(value: str) -> AppResponseOpenapiContractPolicy:
    if value in APP_RESPONSE_OPENAPI_CONTRACT_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_RESPONSE_OPENAPI_CONTRACT_POLICY_VALUES!r}")
