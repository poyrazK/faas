from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_cutover_runtime_drain_member_state import (
    ManagedPostgresCutoverRuntimeDrainMemberState,
    check_managed_postgres_cutover_runtime_drain_member_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresCutoverRuntimeDrainMember")


@_attrs_define
class ManagedPostgresCutoverRuntimeDrainMember:
    """Safe captured workload identity and retirement finding. Host identities and native proof details remain private."""

    instance_id: UUID
    scope: str
    mode: str
    state: ManagedPostgresCutoverRuntimeDrainMemberState
    deployment_id: UUID | Unset = UNSET
    blocker_code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        instance_id = str(self.instance_id)

        scope = self.scope

        mode = self.mode

        state: str = self.state

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        blocker_code = self.blocker_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "instance_id": instance_id,
                "scope": scope,
                "mode": mode,
                "state": state,
            }
        )
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if blocker_code is not UNSET:
            field_dict["blocker_code"] = blocker_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        instance_id = UUID(d.pop("instance_id"))

        scope = d.pop("scope")

        mode = d.pop("mode")

        state = check_managed_postgres_cutover_runtime_drain_member_state(d.pop("state"))

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        blocker_code = d.pop("blocker_code", UNSET)

        managed_postgres_cutover_runtime_drain_member = cls(
            instance_id=instance_id,
            scope=scope,
            mode=mode,
            state=state,
            deployment_id=deployment_id,
            blocker_code=blocker_code,
        )

        managed_postgres_cutover_runtime_drain_member.additional_properties = d
        return managed_postgres_cutover_runtime_drain_member

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
