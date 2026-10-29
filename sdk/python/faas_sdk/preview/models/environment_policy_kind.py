from typing import Literal

EnvironmentPolicyKind = Literal["cors", "headers"]

ENVIRONMENT_POLICY_KIND_VALUES: set[EnvironmentPolicyKind] = {
    "cors",
    "headers",
}


def check_environment_policy_kind(value: str) -> EnvironmentPolicyKind:
    if value in ENVIRONMENT_POLICY_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_POLICY_KIND_VALUES!r}")
