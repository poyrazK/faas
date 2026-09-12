from typing import Literal

APIConsumerUsageStatementResponseStatus = Literal["draft", "finalized"]

API_CONSUMER_USAGE_STATEMENT_RESPONSE_STATUS_VALUES: set[APIConsumerUsageStatementResponseStatus] = {
    "draft",
    "finalized",
}


def check_api_consumer_usage_statement_response_status(value: str) -> APIConsumerUsageStatementResponseStatus:
    if value in API_CONSUMER_USAGE_STATEMENT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {API_CONSUMER_USAGE_STATEMENT_RESPONSE_STATUS_VALUES!r}"
    )
