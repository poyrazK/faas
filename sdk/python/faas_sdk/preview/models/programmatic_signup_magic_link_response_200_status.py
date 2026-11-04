from typing import Literal

ProgrammaticSignupMagicLinkResponse200Status = Literal["ok"]

PROGRAMMATIC_SIGNUP_MAGIC_LINK_RESPONSE_200_STATUS_VALUES: set[ProgrammaticSignupMagicLinkResponse200Status] = {
    "ok",
}


def check_programmatic_signup_magic_link_response_200_status(
    value: str,
) -> ProgrammaticSignupMagicLinkResponse200Status:
    if value in PROGRAMMATIC_SIGNUP_MAGIC_LINK_RESPONSE_200_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROGRAMMATIC_SIGNUP_MAGIC_LINK_RESPONSE_200_STATUS_VALUES!r}"
    )
