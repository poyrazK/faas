from typing import Literal

APISandboxExampleRequestMethod = Literal["DELETE", "GET", "HEAD", "PATCH", "POST", "PUT"]

API_SANDBOX_EXAMPLE_REQUEST_METHOD_VALUES: set[APISandboxExampleRequestMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "PATCH",
    "POST",
    "PUT",
}


def check_api_sandbox_example_request_method(value: str) -> APISandboxExampleRequestMethod:
    if value in API_SANDBOX_EXAMPLE_REQUEST_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SANDBOX_EXAMPLE_REQUEST_METHOD_VALUES!r}")
