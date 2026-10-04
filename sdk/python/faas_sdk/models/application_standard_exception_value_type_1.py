from typing import Literal

ApplicationStandardExceptionValueType1 = Literal["audit", "enforce", "off"]

APPLICATION_STANDARD_EXCEPTION_VALUE_TYPE_1_VALUES: set[ApplicationStandardExceptionValueType1] = {
    "audit",
    "enforce",
    "off",
}


def check_application_standard_exception_value_type_1(value: str) -> ApplicationStandardExceptionValueType1:
    if value in APPLICATION_STANDARD_EXCEPTION_VALUE_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_EXCEPTION_VALUE_TYPE_1_VALUES!r}"
    )
