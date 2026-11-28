from typing import Literal

OperationDoctorResponsePlan = Literal["free", "hobby", "pro", "scale"]

OPERATION_DOCTOR_RESPONSE_PLAN_VALUES: set[OperationDoctorResponsePlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_operation_doctor_response_plan(value: str) -> OperationDoctorResponsePlan:
    if value in OPERATION_DOCTOR_RESPONSE_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_DOCTOR_RESPONSE_PLAN_VALUES!r}")
