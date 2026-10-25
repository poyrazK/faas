from typing import Literal

BindingApplicationAdoptionSource = Literal["application_ack"]

BINDING_APPLICATION_ADOPTION_SOURCE_VALUES: set[BindingApplicationAdoptionSource] = {
    "application_ack",
}


def check_binding_application_adoption_source(value: str) -> BindingApplicationAdoptionSource:
    if value in BINDING_APPLICATION_ADOPTION_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BINDING_APPLICATION_ADOPTION_SOURCE_VALUES!r}")
