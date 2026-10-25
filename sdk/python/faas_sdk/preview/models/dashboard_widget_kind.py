from typing import Literal

DashboardWidgetKind = Literal["app_metric", "custom_metric", "log_count", "slo"]

DASHBOARD_WIDGET_KIND_VALUES: set[DashboardWidgetKind] = {
    "app_metric",
    "custom_metric",
    "log_count",
    "slo",
}


def check_dashboard_widget_kind(value: str) -> DashboardWidgetKind:
    if value in DASHBOARD_WIDGET_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DASHBOARD_WIDGET_KIND_VALUES!r}")
