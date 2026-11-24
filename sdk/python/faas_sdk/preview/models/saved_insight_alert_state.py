from typing import Literal

SavedInsightAlertState = Literal["firing", "ok"]

SAVED_INSIGHT_ALERT_STATE_VALUES: set[SavedInsightAlertState] = {
    "firing",
    "ok",
}


def check_saved_insight_alert_state(value: str) -> SavedInsightAlertState:
    if value in SAVED_INSIGHT_ALERT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SAVED_INSIGHT_ALERT_STATE_VALUES!r}")
