from typing import Literal

AppWebhookResponseEventFilterItem = Literal[
    "app.parked",
    "app.woken",
    "debug.regression.detected",
    "debug.regression.resolved",
    "deployment.failed",
    "deployment.live",
    "issue.assigned",
    "issue.created",
    "issue.ignored",
    "issue.impact_threshold_reached",
    "issue.regressed",
    "issue.reopened",
    "issue.resolved",
    "job.finished",
    "operation.finished",
    "rollout.aborted",
    "rollout.completed",
    "routes.health.aborted",
    "routes.health.blocked",
    "routes.health.resumed",
    "routes.monitor.recovered",
    "routes.monitor.violated",
    "routes.requirements.changed",
    "routes.requirements.recovered",
    "routes.requirements.violated",
    "usage_statement.finalized",
]

APP_WEBHOOK_RESPONSE_EVENT_FILTER_ITEM_VALUES: set[AppWebhookResponseEventFilterItem] = {
    "app.parked",
    "app.woken",
    "debug.regression.detected",
    "debug.regression.resolved",
    "deployment.failed",
    "deployment.live",
    "issue.assigned",
    "issue.created",
    "issue.ignored",
    "issue.impact_threshold_reached",
    "issue.regressed",
    "issue.reopened",
    "issue.resolved",
    "job.finished",
    "operation.finished",
    "rollout.aborted",
    "rollout.completed",
    "routes.health.aborted",
    "routes.health.blocked",
    "routes.health.resumed",
    "routes.monitor.recovered",
    "routes.monitor.violated",
    "routes.requirements.changed",
    "routes.requirements.recovered",
    "routes.requirements.violated",
    "usage_statement.finalized",
}


def check_app_webhook_response_event_filter_item(value: str) -> AppWebhookResponseEventFilterItem:
    if value in APP_WEBHOOK_RESPONSE_EVENT_FILTER_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_WEBHOOK_RESPONSE_EVENT_FILTER_ITEM_VALUES!r}")
