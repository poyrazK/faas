from typing import Literal

DomainReadinessRouting = Literal["fail", "na", "ok", "pending"]

DOMAIN_READINESS_ROUTING_VALUES: set[DomainReadinessRouting] = {
    "fail",
    "na",
    "ok",
    "pending",
}


def check_domain_readiness_routing(value: str) -> DomainReadinessRouting:
    if value in DOMAIN_READINESS_ROUTING_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DOMAIN_READINESS_ROUTING_VALUES!r}")
