from typing import Literal

OperationDoctorResponseObservationScope = Literal["responding_api_node"]

OPERATION_DOCTOR_RESPONSE_OBSERVATION_SCOPE_VALUES: set[OperationDoctorResponseObservationScope] = {
    "responding_api_node",
}


def check_operation_doctor_response_observation_scope(value: str) -> OperationDoctorResponseObservationScope:
    if value in OPERATION_DOCTOR_RESPONSE_OBSERVATION_SCOPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_DOCTOR_RESPONSE_OBSERVATION_SCOPE_VALUES!r}"
    )
