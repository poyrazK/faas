from typing import Literal

ServiceBindingExplanationResult = Literal["allowed", "denied", "indeterminate"]

SERVICE_BINDING_EXPLANATION_RESULT_VALUES: set[ServiceBindingExplanationResult] = {
    "allowed",
    "denied",
    "indeterminate",
}


def check_service_binding_explanation_result(value: str) -> ServiceBindingExplanationResult:
    if value in SERVICE_BINDING_EXPLANATION_RESULT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SERVICE_BINDING_EXPLANATION_RESULT_VALUES!r}")
