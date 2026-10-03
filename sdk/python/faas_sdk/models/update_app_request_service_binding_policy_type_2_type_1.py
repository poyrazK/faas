from typing import Literal

UpdateAppRequestServiceBindingPolicyType2Type1 = Literal["account", "declared"]

UPDATE_APP_REQUEST_SERVICE_BINDING_POLICY_TYPE_2_TYPE_1_VALUES: set[UpdateAppRequestServiceBindingPolicyType2Type1] = {
    "account",
    "declared",
}


def check_update_app_request_service_binding_policy_type_2_type_1(
    value: str,
) -> UpdateAppRequestServiceBindingPolicyType2Type1:
    if value in UPDATE_APP_REQUEST_SERVICE_BINDING_POLICY_TYPE_2_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_SERVICE_BINDING_POLICY_TYPE_2_TYPE_1_VALUES!r}"
    )
