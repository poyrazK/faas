from typing import Literal

ServiceBindingPolicy = Literal["account", "declared"]

SERVICE_BINDING_POLICY_VALUES: set[ServiceBindingPolicy] = {
    "account",
    "declared",
}


def check_service_binding_policy(value: str) -> ServiceBindingPolicy:
    if value in SERVICE_BINDING_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SERVICE_BINDING_POLICY_VALUES!r}")
