from typing import Literal

OperationDoctorCheckExecutionKind = Literal["http", "job", "workflow"]

OPERATION_DOCTOR_CHECK_EXECUTION_KIND_VALUES: set[OperationDoctorCheckExecutionKind] = {
    "http",
    "job",
    "workflow",
}


def check_operation_doctor_check_execution_kind(value: str) -> OperationDoctorCheckExecutionKind:
    if value in OPERATION_DOCTOR_CHECK_EXECUTION_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_DOCTOR_CHECK_EXECUTION_KIND_VALUES!r}")
