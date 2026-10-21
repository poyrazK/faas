from typing import Literal

ApproveApplicationStandardExceptionRequestField = Literal[
    "egress_cidrs", "egress_extra_ports", "log_destinations", "require_signed", "security_policy", "trusted_publishers"
]

APPROVE_APPLICATION_STANDARD_EXCEPTION_REQUEST_FIELD_VALUES: set[ApproveApplicationStandardExceptionRequestField] = {
    "egress_cidrs",
    "egress_extra_ports",
    "log_destinations",
    "require_signed",
    "security_policy",
    "trusted_publishers",
}


def check_approve_application_standard_exception_request_field(
    value: str,
) -> ApproveApplicationStandardExceptionRequestField:
    if value in APPROVE_APPLICATION_STANDARD_EXCEPTION_REQUEST_FIELD_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPROVE_APPLICATION_STANDARD_EXCEPTION_REQUEST_FIELD_VALUES!r}"
    )
