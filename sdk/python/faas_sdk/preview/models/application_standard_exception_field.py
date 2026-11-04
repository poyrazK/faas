from typing import Literal

ApplicationStandardExceptionField = Literal[
    "egress_cidrs", "egress_extra_ports", "log_destinations", "require_signed", "security_policy", "trusted_publishers"
]

APPLICATION_STANDARD_EXCEPTION_FIELD_VALUES: set[ApplicationStandardExceptionField] = {
    "egress_cidrs",
    "egress_extra_ports",
    "log_destinations",
    "require_signed",
    "security_policy",
    "trusted_publishers",
}


def check_application_standard_exception_field(value: str) -> ApplicationStandardExceptionField:
    if value in APPLICATION_STANDARD_EXCEPTION_FIELD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_EXCEPTION_FIELD_VALUES!r}")
