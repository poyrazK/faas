from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ExclusiveWorkPolicyRecord")


@_attrs_define
class ExclusiveWorkPolicyRecord:
    """Persisted policy revision and lifecycle metadata."""

    id: UUID
    revision: int
    policy: Any
    """Account-owned policy for managed exclusive operations. Apps and selected Jobs share the account policy
    namespace; Jobs require account scope and cannot use an app project environment."""
    retired: bool
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        revision = self.revision

        policy: Any
        policy = self.policy

        retired = self.retired

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "revision": revision,
                "policy": policy,
                "retired": retired,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        revision = d.pop("revision")

        def _parse_policy(data: object) -> Any:
            return cast(Any, data)

        policy = _parse_policy(d.pop("policy"))

        retired = d.pop("retired")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        exclusive_work_policy_record = cls(
            id=id,
            revision=revision,
            policy=policy,
            retired=retired,
            created_at=created_at,
            updated_at=updated_at,
        )

        exclusive_work_policy_record.additional_properties = d
        return exclusive_work_policy_record

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
