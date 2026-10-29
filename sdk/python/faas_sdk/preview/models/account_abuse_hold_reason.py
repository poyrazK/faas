from typing import Literal

AccountAbuseHoldReason = Literal["egress_fanout", "egress_flood", "operator"]

ACCOUNT_ABUSE_HOLD_REASON_VALUES: set[AccountAbuseHoldReason] = {
    "egress_fanout",
    "egress_flood",
    "operator",
}


def check_account_abuse_hold_reason(value: str) -> AccountAbuseHoldReason:
    if value in ACCOUNT_ABUSE_HOLD_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ACCOUNT_ABUSE_HOLD_REASON_VALUES!r}")
