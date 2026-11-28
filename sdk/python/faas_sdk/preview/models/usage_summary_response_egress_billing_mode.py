from typing import Literal

UsageSummaryResponseEgressBillingMode = Literal["live", "shadow"]

USAGE_SUMMARY_RESPONSE_EGRESS_BILLING_MODE_VALUES: set[UsageSummaryResponseEgressBillingMode] = {
    "live",
    "shadow",
}


def check_usage_summary_response_egress_billing_mode(value: str) -> UsageSummaryResponseEgressBillingMode:
    if value in USAGE_SUMMARY_RESPONSE_EGRESS_BILLING_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {USAGE_SUMMARY_RESPONSE_EGRESS_BILLING_MODE_VALUES!r}"
    )
