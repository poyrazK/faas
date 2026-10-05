from typing import Literal

OperationDoctorCheckStatus = Literal["blocked", "configured", "not_requested", "observed", "unknown", "warning"]

OPERATION_DOCTOR_CHECK_STATUS_VALUES: set[OperationDoctorCheckStatus] = {
    "blocked",
    "configured",
    "not_requested",
    "observed",
    "unknown",
    "warning",
}


def check_operation_doctor_check_status(value: str) -> OperationDoctorCheckStatus:
    if value in OPERATION_DOCTOR_CHECK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_DOCTOR_CHECK_STATUS_VALUES!r}")
