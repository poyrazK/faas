from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operator_runtime_config_revision_scope import (
    OperatorRuntimeConfigRevisionScope,
    check_operator_runtime_config_revision_scope,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperatorRuntimeConfigRevision")


@_attrs_define
class OperatorRuntimeConfigRevision:
    """One append-only runtime configuration revision."""

    id: int
    key: str
    scope: OperatorRuntimeConfigRevisionScope
    scope_id: str
    version: int
    rollout_percent: int
    old_value: Any
    new_value: Any
    reason: str
    created_at: datetime.datetime
    actor_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        key = self.key

        scope: str = self.scope

        scope_id = self.scope_id

        version = self.version

        rollout_percent = self.rollout_percent

        old_value = self.old_value

        new_value = self.new_value

        reason = self.reason

        created_at = self.created_at.isoformat()

        actor_id = self.actor_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "key": key,
                "scope": scope,
                "scope_id": scope_id,
                "version": version,
                "rollout_percent": rollout_percent,
                "old_value": old_value,
                "new_value": new_value,
                "reason": reason,
                "created_at": created_at,
            }
        )
        if actor_id is not UNSET:
            field_dict["actor_id"] = actor_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        key = d.pop("key")

        scope = check_operator_runtime_config_revision_scope(d.pop("scope"))

        scope_id = d.pop("scope_id")

        version = d.pop("version")

        rollout_percent = d.pop("rollout_percent")

        old_value = d.pop("old_value")

        new_value = d.pop("new_value")

        reason = d.pop("reason")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        actor_id = d.pop("actor_id", UNSET)

        operator_runtime_config_revision = cls(
            id=id,
            key=key,
            scope=scope,
            scope_id=scope_id,
            version=version,
            rollout_percent=rollout_percent,
            old_value=old_value,
            new_value=new_value,
            reason=reason,
            created_at=created_at,
            actor_id=actor_id,
        )

        operator_runtime_config_revision.additional_properties = d
        return operator_runtime_config_revision

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
