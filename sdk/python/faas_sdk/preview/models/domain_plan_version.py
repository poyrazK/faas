from typing import Literal

DomainPlanVersion = Literal[1]

DOMAIN_PLAN_VERSION_VALUES: set[DomainPlanVersion] = {
    1,
}


def check_domain_plan_version(value: int) -> DomainPlanVersion:
    if value in DOMAIN_PLAN_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DOMAIN_PLAN_VERSION_VALUES!r}")
