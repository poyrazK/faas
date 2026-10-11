from typing import Literal

BindingReleasePolicyMode = Literal["enforce", "off"]

BINDING_RELEASE_POLICY_MODE_VALUES: set[BindingReleasePolicyMode] = {
    "enforce",
    "off",
}


def check_binding_release_policy_mode(value: str) -> BindingReleasePolicyMode:
    if value in BINDING_RELEASE_POLICY_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BINDING_RELEASE_POLICY_MODE_VALUES!r}")
