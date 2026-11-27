from typing import Literal

APISandboxSuiteScenarioResultStatus = Literal["inconclusive", "not_run", "passed", "queued", "regression", "running"]

API_SANDBOX_SUITE_SCENARIO_RESULT_STATUS_VALUES: set[APISandboxSuiteScenarioResultStatus] = {
    "inconclusive",
    "not_run",
    "passed",
    "queued",
    "regression",
    "running",
}


def check_api_sandbox_suite_scenario_result_status(value: str) -> APISandboxSuiteScenarioResultStatus:
    if value in API_SANDBOX_SUITE_SCENARIO_RESULT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SANDBOX_SUITE_SCENARIO_RESULT_STATUS_VALUES!r}")
