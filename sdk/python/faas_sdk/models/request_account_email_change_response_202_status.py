from typing import Literal

RequestAccountEmailChangeResponse202Status = Literal["confirmation_sent"]

REQUEST_ACCOUNT_EMAIL_CHANGE_RESPONSE_202_STATUS_VALUES: set[RequestAccountEmailChangeResponse202Status] = {
    "confirmation_sent",
}


def check_request_account_email_change_response_202_status(value: str) -> RequestAccountEmailChangeResponse202Status:
    if value in REQUEST_ACCOUNT_EMAIL_CHANGE_RESPONSE_202_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REQUEST_ACCOUNT_EMAIL_CHANGE_RESPONSE_202_STATUS_VALUES!r}"
    )
