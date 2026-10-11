from typing import Literal

AppHealthRequestsCoverage = Literal["serving_deployments"]

APP_HEALTH_REQUESTS_COVERAGE_VALUES: set[AppHealthRequestsCoverage] = {
    "serving_deployments",
}


def check_app_health_requests_coverage(value: str) -> AppHealthRequestsCoverage:
    if value in APP_HEALTH_REQUESTS_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_HEALTH_REQUESTS_COVERAGE_VALUES!r}")
