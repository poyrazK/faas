from typing import Literal

ObsNotificationReceiverIntegration = Literal[
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

OBS_NOTIFICATION_RECEIVER_INTEGRATION_VALUES: set[ObsNotificationReceiverIntegration] = {
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


def check_obs_notification_receiver_integration(value: str) -> ObsNotificationReceiverIntegration:
    if value in OBS_NOTIFICATION_RECEIVER_INTEGRATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_NOTIFICATION_RECEIVER_INTEGRATION_VALUES!r}")
