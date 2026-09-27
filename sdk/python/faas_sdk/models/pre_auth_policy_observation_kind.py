from typing import Literal

PreAuthPolicyObservationKind = Literal["app", "failures", "route"]

PRE_AUTH_POLICY_OBSERVATION_KIND_VALUES: set[PreAuthPolicyObservationKind] = {
    "app",
    "failures",
    "route",
}


def check_pre_auth_policy_observation_kind(value: str) -> PreAuthPolicyObservationKind:
    if value in PRE_AUTH_POLICY_OBSERVATION_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PRE_AUTH_POLICY_OBSERVATION_KIND_VALUES!r}")
