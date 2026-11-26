from typing import Literal

APIConsumerBillingUsageProgressStatus = Literal[
    "included", "mixed_currency", "not_billable", "priced", "unpriced", "waiting"
]

API_CONSUMER_BILLING_USAGE_PROGRESS_STATUS_VALUES: set[APIConsumerBillingUsageProgressStatus] = {
    "included",
    "mixed_currency",
    "not_billable",
    "priced",
    "unpriced",
    "waiting",
}


def check_api_consumer_billing_usage_progress_status(value: str) -> APIConsumerBillingUsageProgressStatus:
    if value in API_CONSUMER_BILLING_USAGE_PROGRESS_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {API_CONSUMER_BILLING_USAGE_PROGRESS_STATUS_VALUES!r}"
    )
