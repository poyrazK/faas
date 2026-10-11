from typing import Literal

SetBindingReleasePolicyRequestMode = Literal["enforce", "off"]

SET_BINDING_RELEASE_POLICY_REQUEST_MODE_VALUES: set[SetBindingReleasePolicyRequestMode] = {
    "enforce",
    "off",
}


def check_set_binding_release_policy_request_mode(value: str) -> SetBindingReleasePolicyRequestMode:
    if value in SET_BINDING_RELEASE_POLICY_REQUEST_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SET_BINDING_RELEASE_POLICY_REQUEST_MODE_VALUES!r}")
