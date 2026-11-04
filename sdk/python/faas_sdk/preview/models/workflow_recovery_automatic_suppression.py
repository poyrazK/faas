from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="WorkflowRecoveryAutomaticSuppression")


@_attrs_define
class WorkflowRecoveryAutomaticSuppression:
    """Immutable operator suppression audit retained with the automatic retry intent."""

    reason: str
    actor_kind: str
    actor_id: str
    suppressed_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        reason = self.reason

        actor_kind = self.actor_kind

        actor_id = self.actor_id

        suppressed_at = self.suppressed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "reason": reason,
                "actor_kind": actor_kind,
                "actor_id": actor_id,
                "suppressed_at": suppressed_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        reason = d.pop("reason")

        actor_kind = d.pop("actor_kind")

        actor_id = d.pop("actor_id")

        suppressed_at = datetime.datetime.fromisoformat(d.pop("suppressed_at"))

        workflow_recovery_automatic_suppression = cls(
            reason=reason,
            actor_kind=actor_kind,
            actor_id=actor_id,
            suppressed_at=suppressed_at,
        )

        workflow_recovery_automatic_suppression.additional_properties = d
        return workflow_recovery_automatic_suppression

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
