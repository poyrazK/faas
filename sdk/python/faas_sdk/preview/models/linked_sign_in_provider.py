from typing import Literal

LinkedSignInProvider = Literal["github", "google"]

LINKED_SIGN_IN_PROVIDER_VALUES: set[LinkedSignInProvider] = {
    "github",
    "google",
}


def check_linked_sign_in_provider(value: str) -> LinkedSignInProvider:
    if value in LINKED_SIGN_IN_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LINKED_SIGN_IN_PROVIDER_VALUES!r}")
