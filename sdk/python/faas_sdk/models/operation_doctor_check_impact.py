from typing import Literal

OperationDoctorCheckImpact = Literal["delivery", "qualification", "submission"]

OPERATION_DOCTOR_CHECK_IMPACT_VALUES: set[OperationDoctorCheckImpact] = {
    "delivery",
    "qualification",
    "submission",
}


def check_operation_doctor_check_impact(value: str) -> OperationDoctorCheckImpact:
    if value in OPERATION_DOCTOR_CHECK_IMPACT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_DOCTOR_CHECK_IMPACT_VALUES!r}")
