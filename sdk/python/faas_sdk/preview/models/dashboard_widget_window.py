from typing import Literal

DashboardWidgetWindow = Literal["1h", "24h", "5m", "6h", "7d"]

DASHBOARD_WIDGET_WINDOW_VALUES: set[DashboardWidgetWindow] = {
    "1h",
    "24h",
    "5m",
    "6h",
    "7d",
}


def check_dashboard_widget_window(value: str) -> DashboardWidgetWindow:
    if value in DASHBOARD_WIDGET_WINDOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DASHBOARD_WIDGET_WINDOW_VALUES!r}")
