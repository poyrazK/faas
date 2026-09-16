from typing import Literal

UpdateAppRequestOpenapiContractPolicyType2Type1 = Literal["block", "observe", "warn"]

UPDATE_APP_REQUEST_OPENAPI_CONTRACT_POLICY_TYPE_2_TYPE_1_VALUES: set[
    UpdateAppRequestOpenapiContractPolicyType2Type1
] = {
    "block",
    "observe",
    "warn",
}


def check_update_app_request_openapi_contract_policy_type_2_type_1(
    value: str,
) -> UpdateAppRequestOpenapiContractPolicyType2Type1:
    if value in UPDATE_APP_REQUEST_OPENAPI_CONTRACT_POLICY_TYPE_2_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_OPENAPI_CONTRACT_POLICY_TYPE_2_TYPE_1_VALUES!r}"
    )
