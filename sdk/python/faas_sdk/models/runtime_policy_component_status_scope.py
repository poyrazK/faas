from typing import Literal

RuntimePolicyComponentStatusScope = Literal["account", "app"]

RUNTIME_POLICY_COMPONENT_STATUS_SCOPE_VALUES: set[RuntimePolicyComponentStatusScope] = {
    "account",
    "app",
}


def check_runtime_policy_component_status_scope(value: str) -> RuntimePolicyComponentStatusScope:
    if value in RUNTIME_POLICY_COMPONENT_STATUS_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUNTIME_POLICY_COMPONENT_STATUS_SCOPE_VALUES!r}")
