from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.condition_status import ConditionStatus, check_condition_status
from ..models.condition_type import ConditionType, check_condition_type
from ..types import UNSET, Unset

T = TypeVar("T", bound="Condition")


@_attrs_define
class Condition:
    """One aspect of a resource's state, in the shape of a Kubernetes status condition."""

    type_: ConditionType
    status: ConditionStatus
    reason: str
    """Stable snake_case code, safe to branch on: an app health phase or check code; a deployment, database,
    certificate or job run status; a cron suspension or run outcome; a database health error code; or one of
    not_assessed, deploying, complete, latest_deployment_failed, healthy, verifying, rolling_out, rolled_out,
    rollout_aborted, suspended, stale, verified, dns_pending, serving, scheduled, disabled, never_run."""
    message: str
    """For people; may change."""
    since: datetime.datetime | Unset = UNSET
    """When the condition last changed"""
    terminal: bool | Unset = UNSET
    """True when the condition will not change without a new request."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_: str = self.type_

        status: str = self.status

        reason = self.reason

        message = self.message

        since: str | Unset = UNSET
        if not isinstance(self.since, Unset):
            since = self.since.isoformat()

        terminal = self.terminal

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "type": type_,
                "status": status,
                "reason": reason,
                "message": message,
            }
        )
        if since is not UNSET:
            field_dict["since"] = since
        if terminal is not UNSET:
            field_dict["terminal"] = terminal

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        type_ = check_condition_type(d.pop("type"))

        status = check_condition_status(d.pop("status"))

        reason = d.pop("reason")

        message = d.pop("message")

        _since = d.pop("since", UNSET)
        since: datetime.datetime | Unset
        if isinstance(_since, Unset):
            since = UNSET
        else:
            since = datetime.datetime.fromisoformat(_since)

        terminal = d.pop("terminal", UNSET)

        condition = cls(
            type_=type_,
            status=status,
            reason=reason,
            message=message,
            since=since,
            terminal=terminal,
        )

        condition.additional_properties = d
        return condition

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
