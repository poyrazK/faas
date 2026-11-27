from typing import Literal

APIConsumerBillingStatementProgressStatus = Literal["draft", "finalized"]

API_CONSUMER_BILLING_STATEMENT_PROGRESS_STATUS_VALUES: set[APIConsumerBillingStatementProgressStatus] = {
    "draft",
    "finalized",
}


def check_api_consumer_billing_statement_progress_status(value: str) -> APIConsumerBillingStatementProgressStatus:
    if value in API_CONSUMER_BILLING_STATEMENT_PROGRESS_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {API_CONSUMER_BILLING_STATEMENT_PROGRESS_STATUS_VALUES!r}"
    )
