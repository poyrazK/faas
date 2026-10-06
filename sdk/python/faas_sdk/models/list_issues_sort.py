from typing import Literal

ListIssuesSort = Literal["impact", "recent"]

LIST_ISSUES_SORT_VALUES: set[ListIssuesSort] = {
    "impact",
    "recent",
}


def check_list_issues_sort(value: str) -> ListIssuesSort:
    if value in LIST_ISSUES_SORT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_ISSUES_SORT_VALUES!r}")
