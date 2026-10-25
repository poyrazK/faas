from typing import Literal

UpdateAppRequestServiceBindingPolicyType3Type1 = Literal["account", "declared"]

UPDATE_APP_REQUEST_SERVICE_BINDING_POLICY_TYPE_3_TYPE_1_VALUES: set[UpdateAppRequestServiceBindingPolicyType3Type1] = {
    "account",
    "declared",
}


def check_update_app_request_service_binding_policy_type_3_type_1(
    value: str,
) -> UpdateAppRequestServiceBindingPolicyType3Type1:
    if value in UPDATE_APP_REQUEST_SERVICE_BINDING_POLICY_TYPE_3_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_SERVICE_BINDING_POLICY_TYPE_3_TYPE_1_VALUES!r}"
    )
