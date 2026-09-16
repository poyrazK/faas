from typing import Literal

CreateAppRequestOpenapiContractPolicy = Literal["block", "observe", "warn"]

CREATE_APP_REQUEST_OPENAPI_CONTRACT_POLICY_VALUES: set[CreateAppRequestOpenapiContractPolicy] = {
    "block",
    "observe",
    "warn",
}


def check_create_app_request_openapi_contract_policy(value: str) -> CreateAppRequestOpenapiContractPolicy:
    if value in CREATE_APP_REQUEST_OPENAPI_CONTRACT_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_APP_REQUEST_OPENAPI_CONTRACT_POLICY_VALUES!r}"
    )
