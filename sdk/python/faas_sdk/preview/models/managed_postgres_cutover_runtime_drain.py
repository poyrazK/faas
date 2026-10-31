from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_cutover_runtime_drain_state import (
    ManagedPostgresCutoverRuntimeDrainState,
    check_managed_postgres_cutover_runtime_drain_state,
)

if TYPE_CHECKING:
    from ..models.managed_postgres_cutover_runtime_drain_member import ManagedPostgresCutoverRuntimeDrainMember


T = TypeVar("T", bound="ManagedPostgresCutoverRuntimeDrain")


@_attrs_define
class ManagedPostgresCutoverRuntimeDrain:
    """Historical scheduler instance catalog evidence at checked_at. Catalog retirement does not prove complete writer
    isolation. No fence or drain is initiated by preflight. Absent on pending requests and reports created before
    runtime observations were supported.

    """

    state: ManagedPostgresCutoverRuntimeDrainState
    inventory_count: int
    """Captured catalog count; a lower bound when inventory_limit_exceeded is reported."""
    pending: int
    retired: int
    """Members with matching native retirement receipts."""
    blocked: int
    blockers: list[str]
    """Plan and authority blocker codes. Member blockers appear on each member."""
    members: list[ManagedPostgresCutoverRuntimeDrainMember]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        inventory_count = self.inventory_count

        pending = self.pending

        retired = self.retired

        blocked = self.blocked

        blockers = self.blockers

        members = []
        for members_item_data in self.members:
            members_item = members_item_data.to_dict()
            members.append(members_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "inventory_count": inventory_count,
                "pending": pending,
                "retired": retired,
                "blocked": blocked,
                "blockers": blockers,
                "members": members,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_cutover_runtime_drain_member import ManagedPostgresCutoverRuntimeDrainMember

        d = dict(src_dict)
        state = check_managed_postgres_cutover_runtime_drain_state(d.pop("state"))

        inventory_count = d.pop("inventory_count")

        pending = d.pop("pending")

        retired = d.pop("retired")

        blocked = d.pop("blocked")

        blockers = cast(list[str], d.pop("blockers"))

        members = []
        _members = d.pop("members")
        for members_item_data in _members:
            members_item = ManagedPostgresCutoverRuntimeDrainMember.from_dict(members_item_data)

            members.append(members_item)

        managed_postgres_cutover_runtime_drain = cls(
            state=state,
            inventory_count=inventory_count,
            pending=pending,
            retired=retired,
            blocked=blocked,
            blockers=blockers,
            members=members,
        )

        managed_postgres_cutover_runtime_drain.additional_properties = d
        return managed_postgres_cutover_runtime_drain

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
