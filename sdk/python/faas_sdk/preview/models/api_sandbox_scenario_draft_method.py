from typing import Literal

APISandboxScenarioDraftMethod = Literal["DELETE", "GET", "HEAD", "PATCH", "POST", "PUT"]

API_SANDBOX_SCENARIO_DRAFT_METHOD_VALUES: set[APISandboxScenarioDraftMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "PATCH",
    "POST",
    "PUT",
}


def check_api_sandbox_scenario_draft_method(value: str) -> APISandboxScenarioDraftMethod:
    if value in API_SANDBOX_SCENARIO_DRAFT_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SANDBOX_SCENARIO_DRAFT_METHOD_VALUES!r}")
