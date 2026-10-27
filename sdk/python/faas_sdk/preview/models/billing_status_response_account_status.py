from typing import Literal

BillingStatusResponseAccountStatus = Literal["active", "deleted_pending", "past_due", "suspended"]

BILLING_STATUS_RESPONSE_ACCOUNT_STATUS_VALUES: set[BillingStatusResponseAccountStatus] = {
    "active",
    "deleted_pending",
    "past_due",
    "suspended",
}


def check_billing_status_response_account_status(value: str) -> BillingStatusResponseAccountStatus:
    if value in BILLING_STATUS_RESPONSE_ACCOUNT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_STATUS_RESPONSE_ACCOUNT_STATUS_VALUES!r}")
