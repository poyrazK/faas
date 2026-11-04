from typing import Literal

SubmitSalesInquiryBodyIntent = Literal["contact", "demo"]

SUBMIT_SALES_INQUIRY_BODY_INTENT_VALUES: set[SubmitSalesInquiryBodyIntent] = {
    "contact",
    "demo",
}


def check_submit_sales_inquiry_body_intent(value: str) -> SubmitSalesInquiryBodyIntent:
    if value in SUBMIT_SALES_INQUIRY_BODY_INTENT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SUBMIT_SALES_INQUIRY_BODY_INTENT_VALUES!r}")
