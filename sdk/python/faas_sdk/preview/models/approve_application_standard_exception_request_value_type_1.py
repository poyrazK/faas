from typing import Literal

ApproveApplicationStandardExceptionRequestValueType1 = Literal["enforce", "off", "warn"]

APPROVE_APPLICATION_STANDARD_EXCEPTION_REQUEST_VALUE_TYPE_1_VALUES: set[
    ApproveApplicationStandardExceptionRequestValueType1
] = {
    "enforce",
    "off",
    "warn",
}


def check_approve_application_standard_exception_request_value_type_1(
    value: str,
) -> ApproveApplicationStandardExceptionRequestValueType1:
    if value in APPROVE_APPLICATION_STANDARD_EXCEPTION_REQUEST_VALUE_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPROVE_APPLICATION_STANDARD_EXCEPTION_REQUEST_VALUE_TYPE_1_VALUES!r}"
    )
