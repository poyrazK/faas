from typing import Literal

BindingVerificationRunSummaryStatus = Literal["cancelled", "expired", "passed", "pending"]

BINDING_VERIFICATION_RUN_SUMMARY_STATUS_VALUES: set[BindingVerificationRunSummaryStatus] = {
    "cancelled",
    "expired",
    "passed",
    "pending",
}


def check_binding_verification_run_summary_status(value: str) -> BindingVerificationRunSummaryStatus:
    if value in BINDING_VERIFICATION_RUN_SUMMARY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BINDING_VERIFICATION_RUN_SUMMARY_STATUS_VALUES!r}")
