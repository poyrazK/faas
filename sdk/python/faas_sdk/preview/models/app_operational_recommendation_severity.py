from typing import Literal

AppOperationalRecommendationSeverity = Literal["error", "info", "warning"]

APP_OPERATIONAL_RECOMMENDATION_SEVERITY_VALUES: set[AppOperationalRecommendationSeverity] = {
    "error",
    "info",
    "warning",
}


def check_app_operational_recommendation_severity(value: str) -> AppOperationalRecommendationSeverity:
    if value in APP_OPERATIONAL_RECOMMENDATION_SEVERITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_OPERATIONAL_RECOMMENDATION_SEVERITY_VALUES!r}")
