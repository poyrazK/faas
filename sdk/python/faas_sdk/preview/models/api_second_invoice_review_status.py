from typing import Literal

APISecondInvoiceReviewStatus = Literal[
    "awaiting_payment",
    "paid",
    "ready_to_export",
    "review_invoice",
    "statement_draft",
    "stripe_draft",
    "test_invoice",
    "usage_hold",
]

API_SECOND_INVOICE_REVIEW_STATUS_VALUES: set[APISecondInvoiceReviewStatus] = {
    "awaiting_payment",
    "paid",
    "ready_to_export",
    "review_invoice",
    "statement_draft",
    "stripe_draft",
    "test_invoice",
    "usage_hold",
}


def check_api_second_invoice_review_status(value: str) -> APISecondInvoiceReviewStatus:
    if value in API_SECOND_INVOICE_REVIEW_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SECOND_INVOICE_REVIEW_STATUS_VALUES!r}")
