from typing import Literal

ServiceBindingExplanationPolicySource = Literal["app", "environment_release"]

SERVICE_BINDING_EXPLANATION_POLICY_SOURCE_VALUES: set[ServiceBindingExplanationPolicySource] = {
    "app",
    "environment_release",
}


def check_service_binding_explanation_policy_source(value: str) -> ServiceBindingExplanationPolicySource:
    if value in SERVICE_BINDING_EXPLANATION_POLICY_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SERVICE_BINDING_EXPLANATION_POLICY_SOURCE_VALUES!r}")
