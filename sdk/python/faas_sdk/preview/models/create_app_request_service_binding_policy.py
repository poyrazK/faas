from typing import Literal

CreateAppRequestServiceBindingPolicy = Literal["account", "declared"]

CREATE_APP_REQUEST_SERVICE_BINDING_POLICY_VALUES: set[CreateAppRequestServiceBindingPolicy] = {
    "account",
    "declared",
}


def check_create_app_request_service_binding_policy(value: str) -> CreateAppRequestServiceBindingPolicy:
    if value in CREATE_APP_REQUEST_SERVICE_BINDING_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_APP_REQUEST_SERVICE_BINDING_POLICY_VALUES!r}")
