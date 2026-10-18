from typing import Literal

IssueSavedViewFiltersSort = Literal["impact", "recent"]

ISSUE_SAVED_VIEW_FILTERS_SORT_VALUES: set[IssueSavedViewFiltersSort] = {
    "impact",
    "recent",
}


def check_issue_saved_view_filters_sort(value: str) -> IssueSavedViewFiltersSort:
    if value in ISSUE_SAVED_VIEW_FILTERS_SORT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_SAVED_VIEW_FILTERS_SORT_VALUES!r}")
