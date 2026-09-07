from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RolloutAbortedWebhookPayload")


@_attrs_define
class RolloutAbortedWebhookPayload:
    """Rollout abort details delivered with rollout.aborted."""

    app_id: str
    deployment_id: str
    reason: str
    aborted_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        deployment_id = self.deployment_id

        reason = self.reason

        aborted_at: str | Unset = UNSET
        if not isinstance(self.aborted_at, Unset):
            aborted_at = self.aborted_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "deployment_id": deployment_id,
                "reason": reason,
            }
        )
        if aborted_at is not UNSET:
            field_dict["aborted_at"] = aborted_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        deployment_id = d.pop("deployment_id")

        reason = d.pop("reason")

        _aborted_at = d.pop("aborted_at", UNSET)
        aborted_at: datetime.datetime | Unset
        if isinstance(_aborted_at, Unset):
            aborted_at = UNSET
        else:
            aborted_at = datetime.datetime.fromisoformat(_aborted_at)

        rollout_aborted_webhook_payload = cls(
            app_id=app_id,
            deployment_id=deployment_id,
            reason=reason,
            aborted_at=aborted_at,
        )

        rollout_aborted_webhook_payload.additional_properties = d
        return rollout_aborted_webhook_payload

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
