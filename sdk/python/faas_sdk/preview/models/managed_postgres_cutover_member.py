from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_cutover_member_access import (
    ManagedPostgresCutoverMemberAccess,
    check_managed_postgres_cutover_member_access,
)
from ..models.managed_postgres_cutover_member_state import (
    ManagedPostgresCutoverMemberState,
    check_managed_postgres_cutover_member_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresCutoverMember")


@_attrs_define
class ManagedPostgresCutoverMember:
    """Safe staging and SQL verification evidence for a source binding."""

    source_binding_id: UUID
    environment_key: str
    access: ManagedPostgresCutoverMemberAccess
    state: ManagedPostgresCutoverMemberState
    verified_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source_binding_id = str(self.source_binding_id)

        environment_key = self.environment_key

        access: str = self.access

        state: str = self.state

        verified_at: str | Unset = UNSET
        if not isinstance(self.verified_at, Unset):
            verified_at = self.verified_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source_binding_id": source_binding_id,
                "environment_key": environment_key,
                "access": access,
                "state": state,
            }
        )
        if verified_at is not UNSET:
            field_dict["verified_at"] = verified_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source_binding_id = UUID(d.pop("source_binding_id"))

        environment_key = d.pop("environment_key")

        access = check_managed_postgres_cutover_member_access(d.pop("access"))

        state = check_managed_postgres_cutover_member_state(d.pop("state"))

        _verified_at = d.pop("verified_at", UNSET)
        verified_at: datetime.datetime | Unset
        if isinstance(_verified_at, Unset):
            verified_at = UNSET
        else:
            verified_at = datetime.datetime.fromisoformat(_verified_at)

        managed_postgres_cutover_member = cls(
            source_binding_id=source_binding_id,
            environment_key=environment_key,
            access=access,
            state=state,
            verified_at=verified_at,
        )

        managed_postgres_cutover_member.additional_properties = d
        return managed_postgres_cutover_member

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
