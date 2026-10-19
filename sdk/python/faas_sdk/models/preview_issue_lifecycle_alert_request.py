from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.preview_issue_lifecycle_alert_request_event import (
    PreviewIssueLifecycleAlertRequestEvent,
    check_preview_issue_lifecycle_alert_request_event,
)

T = TypeVar("T", bound="PreviewIssueLifecycleAlertRequest")


@_attrs_define
class PreviewIssueLifecycleAlertRequest:
    """Draft policy and hypothetical lifecycle event for an existing issue."""

    issue_id: UUID
    event: PreviewIssueLifecycleAlertRequestEvent
    environments: list[str]
    """Draft environment allowlist used only for this preview; an empty array matches all."""
    cooldown_seconds: int

    def to_dict(self) -> dict[str, Any]:
        issue_id = str(self.issue_id)

        event: str = self.event

        environments = self.environments

        cooldown_seconds = self.cooldown_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "issue_id": issue_id,
                "event": event,
                "environments": environments,
                "cooldown_seconds": cooldown_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        issue_id = UUID(d.pop("issue_id"))

        event = check_preview_issue_lifecycle_alert_request_event(d.pop("event"))

        environments = cast(list[str], d.pop("environments"))

        cooldown_seconds = d.pop("cooldown_seconds")

        preview_issue_lifecycle_alert_request = cls(
            issue_id=issue_id,
            event=event,
            environments=environments,
            cooldown_seconds=cooldown_seconds,
        )

        return preview_issue_lifecycle_alert_request
