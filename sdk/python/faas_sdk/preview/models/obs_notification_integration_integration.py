from typing import Literal

ObsNotificationIntegrationIntegration = Literal[
    "discord",
    "email",
    "jira",
    "msteams",
    "msteamsv2",
    "opsgenie",
    "pagerduty",
    "pushover",
    "rocketchat",
    "slack",
    "sns",
    "telegram",
    "victorops",
    "webex",
    "webhook",
    "wechat",
]

OBS_NOTIFICATION_INTEGRATION_INTEGRATION_VALUES: set[ObsNotificationIntegrationIntegration] = {
    "discord",
    "email",
    "jira",
    "msteams",
    "msteamsv2",
    "opsgenie",
    "pagerduty",
    "pushover",
    "rocketchat",
    "slack",
    "sns",
    "telegram",
    "victorops",
    "webex",
    "webhook",
    "wechat",
}


def check_obs_notification_integration_integration(value: str) -> ObsNotificationIntegrationIntegration:
    if value in OBS_NOTIFICATION_INTEGRATION_INTEGRATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_NOTIFICATION_INTEGRATION_INTEGRATION_VALUES!r}")
