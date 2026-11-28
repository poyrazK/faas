from typing import Literal

DomainReadinessOwnership = Literal["expired", "pending", "verified"]

DOMAIN_READINESS_OWNERSHIP_VALUES: set[DomainReadinessOwnership] = {
    "expired",
    "pending",
    "verified",
}


def check_domain_readiness_ownership(value: str) -> DomainReadinessOwnership:
    if value in DOMAIN_READINESS_OWNERSHIP_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DOMAIN_READINESS_OWNERSHIP_VALUES!r}")
