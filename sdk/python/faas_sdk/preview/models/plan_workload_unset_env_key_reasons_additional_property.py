from typing import Literal

PlanWorkloadUnsetEnvKeyReasonsAdditionalProperty = Literal[
    "empty", "host_value", "interpolated", "invalid", "local_address", "not_opted_in", "secret", "service_address"
]

PLAN_WORKLOAD_UNSET_ENV_KEY_REASONS_ADDITIONAL_PROPERTY_VALUES: set[
    PlanWorkloadUnsetEnvKeyReasonsAdditionalProperty
] = {
    "empty",
    "host_value",
    "interpolated",
    "invalid",
    "local_address",
    "not_opted_in",
    "secret",
    "service_address",
}


def check_plan_workload_unset_env_key_reasons_additional_property(
    value: str,
) -> PlanWorkloadUnsetEnvKeyReasonsAdditionalProperty:
    if value in PLAN_WORKLOAD_UNSET_ENV_KEY_REASONS_ADDITIONAL_PROPERTY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLAN_WORKLOAD_UNSET_ENV_KEY_REASONS_ADDITIONAL_PROPERTY_VALUES!r}"
    )
