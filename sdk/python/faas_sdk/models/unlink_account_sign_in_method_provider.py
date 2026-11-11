from typing import Literal

UnlinkAccountSignInMethodProvider = Literal["github", "google"]

UNLINK_ACCOUNT_SIGN_IN_METHOD_PROVIDER_VALUES: set[UnlinkAccountSignInMethodProvider] = {
    "github",
    "google",
}


def check_unlink_account_sign_in_method_provider(value: str) -> UnlinkAccountSignInMethodProvider:
    if value in UNLINK_ACCOUNT_SIGN_IN_METHOD_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UNLINK_ACCOUNT_SIGN_IN_METHOD_PROVIDER_VALUES!r}")
