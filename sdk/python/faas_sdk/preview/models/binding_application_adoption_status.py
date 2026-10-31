from typing import Literal

BindingApplicationAdoptionStatus = Literal["current", "failed", "inactive", "stale", "unknown"]

BINDING_APPLICATION_ADOPTION_STATUS_VALUES: set[BindingApplicationAdoptionStatus] = {
    "current",
    "failed",
    "inactive",
    "stale",
    "unknown",
}


def check_binding_application_adoption_status(value: str) -> BindingApplicationAdoptionStatus:
    if value in BINDING_APPLICATION_ADOPTION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BINDING_APPLICATION_ADOPTION_STATUS_VALUES!r}")
