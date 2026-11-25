from typing import Literal

RunProjectSavedInsightFormat = Literal["csv", "json"]

RUN_PROJECT_SAVED_INSIGHT_FORMAT_VALUES: set[RunProjectSavedInsightFormat] = {
    "csv",
    "json",
}


def check_run_project_saved_insight_format(value: str) -> RunProjectSavedInsightFormat:
    if value in RUN_PROJECT_SAVED_INSIGHT_FORMAT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUN_PROJECT_SAVED_INSIGHT_FORMAT_VALUES!r}")
