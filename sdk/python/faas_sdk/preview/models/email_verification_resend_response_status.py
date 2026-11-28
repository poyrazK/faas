from typing import Literal

EmailVerificationResendResponseStatus = Literal["already_verified", "sent"]

EMAIL_VERIFICATION_RESEND_RESPONSE_STATUS_VALUES: set[EmailVerificationResendResponseStatus] = {
    "already_verified",
    "sent",
}


def check_email_verification_resend_response_status(value: str) -> EmailVerificationResendResponseStatus:
    if value in EMAIL_VERIFICATION_RESEND_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EMAIL_VERIFICATION_RESEND_RESPONSE_STATUS_VALUES!r}")
