from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugStatusDeployment")


@_attrs_define
class DebugStatusDeployment:
    id: UUID
    traffic_percent: int
    created_at: datetime.datetime
    rollout_state: str | Unset = UNSET
    commit_sha: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        traffic_percent = self.traffic_percent

        created_at = self.created_at.isoformat()

        rollout_state = self.rollout_state

        commit_sha = self.commit_sha

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "traffic_percent": traffic_percent,
                "created_at": created_at,
            }
        )
        if rollout_state is not UNSET:
            field_dict["rollout_state"] = rollout_state
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        traffic_percent = d.pop("traffic_percent")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        rollout_state = d.pop("rollout_state", UNSET)

        commit_sha = d.pop("commit_sha", UNSET)

        debug_status_deployment = cls(
            id=id,
            traffic_percent=traffic_percent,
            created_at=created_at,
            rollout_state=rollout_state,
            commit_sha=commit_sha,
        )

        debug_status_deployment.additional_properties = d
        return debug_status_deployment

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
