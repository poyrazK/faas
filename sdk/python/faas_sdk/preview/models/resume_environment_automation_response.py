from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ResumeEnvironmentAutomationResponse")


@_attrs_define
class ResumeEnvironmentAutomationResponse:
    """Crons, event subscriptions and bucket notifications enabled by resuming a stage's automation after a full
    environment clone.

    """

    environment: str
    """Stage whose paused automation was enabled."""
    crons_resumed: int
    """Number of crons enabled."""
    cron_ids: list[str]
    """Crons enabled by this call."""
    event_subscriptions_resumed: int
    """Number of event subscriptions enabled."""
    event_subscription_ids: list[str]
    """Event subscriptions enabled by this call."""
    bucket_notifications_resumed: int
    """Number of buckets whose copied notification rules were resumed."""
    bucket_ids: list[str]
    """Buckets whose notification rules this call resumed."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        environment = self.environment

        crons_resumed = self.crons_resumed

        cron_ids = self.cron_ids

        event_subscriptions_resumed = self.event_subscriptions_resumed

        event_subscription_ids = self.event_subscription_ids

        bucket_notifications_resumed = self.bucket_notifications_resumed

        bucket_ids = self.bucket_ids

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "environment": environment,
                "crons_resumed": crons_resumed,
                "cron_ids": cron_ids,
                "event_subscriptions_resumed": event_subscriptions_resumed,
                "event_subscription_ids": event_subscription_ids,
                "bucket_notifications_resumed": bucket_notifications_resumed,
                "bucket_ids": bucket_ids,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        environment = d.pop("environment")

        crons_resumed = d.pop("crons_resumed")

        cron_ids = cast(list[str], d.pop("cron_ids"))

        event_subscriptions_resumed = d.pop("event_subscriptions_resumed")

        event_subscription_ids = cast(list[str], d.pop("event_subscription_ids"))

        bucket_notifications_resumed = d.pop("bucket_notifications_resumed")

        bucket_ids = cast(list[str], d.pop("bucket_ids"))

        resume_environment_automation_response = cls(
            environment=environment,
            crons_resumed=crons_resumed,
            cron_ids=cron_ids,
            event_subscriptions_resumed=event_subscriptions_resumed,
            event_subscription_ids=event_subscription_ids,
            bucket_notifications_resumed=bucket_notifications_resumed,
            bucket_ids=bucket_ids,
        )

        resume_environment_automation_response.additional_properties = d
        return resume_environment_automation_response

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
