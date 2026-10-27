from typing import Literal

PlanWorkloadDependsOnConditionsAdditionalProperty = Literal["service_healthy", "service_started"]

PLAN_WORKLOAD_DEPENDS_ON_CONDITIONS_ADDITIONAL_PROPERTY_VALUES: set[
    PlanWorkloadDependsOnConditionsAdditionalProperty
] = {
    "service_healthy",
    "service_started",
}


def check_plan_workload_depends_on_conditions_additional_property(
    value: str,
) -> PlanWorkloadDependsOnConditionsAdditionalProperty:
    if value in PLAN_WORKLOAD_DEPENDS_ON_CONDITIONS_ADDITIONAL_PROPERTY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLAN_WORKLOAD_DEPENDS_ON_CONDITIONS_ADDITIONAL_PROPERTY_VALUES!r}"
    )
