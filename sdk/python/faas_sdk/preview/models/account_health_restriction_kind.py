from typing import Literal

AccountHealthRestrictionKind = Literal[
    "abuse_hold",
    "account_past_due",
    "account_suspended",
    "deletion_pending",
    "email_unverified",
    "overage_cap_reached",
    "platform_release_pause",
]

ACCOUNT_HEALTH_RESTRICTION_KIND_VALUES: set[AccountHealthRestrictionKind] = {
    "abuse_hold",
    "account_past_due",
    "account_suspended",
    "deletion_pending",
    "email_unverified",
    "overage_cap_reached",
    "platform_release_pause",
}


def check_account_health_restriction_kind(value: str) -> AccountHealthRestrictionKind:
    if value in ACCOUNT_HEALTH_RESTRICTION_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ACCOUNT_HEALTH_RESTRICTION_KIND_VALUES!r}")
