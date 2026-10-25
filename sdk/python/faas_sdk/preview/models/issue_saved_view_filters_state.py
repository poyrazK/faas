from typing import Literal

IssueSavedViewFiltersState = Literal["ignored", "open", "resolved"]

ISSUE_SAVED_VIEW_FILTERS_STATE_VALUES: set[IssueSavedViewFiltersState] = {
    "ignored",
    "open",
    "resolved",
}


def check_issue_saved_view_filters_state(value: str) -> IssueSavedViewFiltersState:
    if value in ISSUE_SAVED_VIEW_FILTERS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_SAVED_VIEW_FILTERS_STATE_VALUES!r}")
