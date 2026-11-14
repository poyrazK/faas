from typing import Literal

DomainReadinessStatus = Literal[
    "awaiting_ownership", "checking_dns", "expired", "fix_routing", "needs_attention", "preparing_https", "ready"
]

DOMAIN_READINESS_STATUS_VALUES: set[DomainReadinessStatus] = {
    "awaiting_ownership",
    "checking_dns",
    "expired",
    "fix_routing",
    "needs_attention",
    "preparing_https",
    "ready",
}


def check_domain_readiness_status(value: str) -> DomainReadinessStatus:
    if value in DOMAIN_READINESS_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DOMAIN_READINESS_STATUS_VALUES!r}")
