from typing import Literal

CheckoutReferralStatusResponse200Reason = Literal["already_attributed", "not_first_purchase", "unavailable"]

CHECKOUT_REFERRAL_STATUS_RESPONSE_200_REASON_VALUES: set[CheckoutReferralStatusResponse200Reason] = {
    "already_attributed",
    "not_first_purchase",
    "unavailable",
}


def check_checkout_referral_status_response_200_reason(value: str) -> CheckoutReferralStatusResponse200Reason:
    if value in CHECKOUT_REFERRAL_STATUS_RESPONSE_200_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CHECKOUT_REFERRAL_STATUS_RESPONSE_200_REASON_VALUES!r}"
    )
