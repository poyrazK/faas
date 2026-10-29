from typing import Literal

ReleaseFreezeWeeklyDaysItem = Literal["fri", "mon", "sat", "sun", "thu", "tue", "wed"]

RELEASE_FREEZE_WEEKLY_DAYS_ITEM_VALUES: set[ReleaseFreezeWeeklyDaysItem] = {
    "fri",
    "mon",
    "sat",
    "sun",
    "thu",
    "tue",
    "wed",
}


def check_release_freeze_weekly_days_item(value: str) -> ReleaseFreezeWeeklyDaysItem:
    if value in RELEASE_FREEZE_WEEKLY_DAYS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RELEASE_FREEZE_WEEKLY_DAYS_ITEM_VALUES!r}")
