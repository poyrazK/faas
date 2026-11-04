from typing import Literal

CapabilitiesResponsePlan = Literal["free", "hobby", "pro", "scale"]

CAPABILITIES_RESPONSE_PLAN_VALUES: set[CapabilitiesResponsePlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_capabilities_response_plan(value: str) -> CapabilitiesResponsePlan:
    if value in CAPABILITIES_RESPONSE_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CAPABILITIES_RESPONSE_PLAN_VALUES!r}")
