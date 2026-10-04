from typing import Literal

CreateAppWebhookRequestEventFilterItem = Literal[
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

CREATE_APP_WEBHOOK_REQUEST_EVENT_FILTER_ITEM_VALUES: set[CreateAppWebhookRequestEventFilterItem] = {
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


def check_create_app_webhook_request_event_filter_item(value: str) -> CreateAppWebhookRequestEventFilterItem:
    if value in CREATE_APP_WEBHOOK_REQUEST_EVENT_FILTER_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_APP_WEBHOOK_REQUEST_EVENT_FILTER_ITEM_VALUES!r}"
    )
