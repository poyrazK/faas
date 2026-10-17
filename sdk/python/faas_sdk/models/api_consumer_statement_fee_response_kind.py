from typing import Literal

APIConsumerStatementFeeResponseKind = Literal["base_fee", "minimum"]

API_CONSUMER_STATEMENT_FEE_RESPONSE_KIND_VALUES: set[APIConsumerStatementFeeResponseKind] = {
    "base_fee",
    "minimum",
}


def check_api_consumer_statement_fee_response_kind(value: str) -> APIConsumerStatementFeeResponseKind:
    if value in API_CONSUMER_STATEMENT_FEE_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_CONSUMER_STATEMENT_FEE_RESPONSE_KIND_VALUES!r}")
