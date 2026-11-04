from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_cutover_state import ManagedPostgresCutoverState, check_managed_postgres_cutover_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_cutover_member import ManagedPostgresCutoverMember


T = TypeVar("T", bound="ManagedPostgresCutover")


@_attrs_define
class ManagedPostgresCutover:
    """Staged cutover metadata. Verified means control-plane SQL evidence; workloads still use the source. Provider
    identities and sealed credentials are never returned.

    """

    id: UUID
    source_database_id: UUID
    target_database_id: UUID
    app_id: UUID
    scope: str
    state: ManagedPostgresCutoverState
    verification_fresh: bool
    """All staged members have SQL evidence no older than verification_max_age_seconds."""
    verification_max_age_seconds: int
    members: list[ManagedPostgresCutoverMember]
    created_at: datetime.datetime
    updated_at: datetime.datetime
    verified_at: datetime.datetime | Unset = UNSET
    last_error_code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        source_database_id = str(self.source_database_id)

        target_database_id = str(self.target_database_id)

        app_id = str(self.app_id)

        scope = self.scope

        state: str = self.state

        verification_fresh = self.verification_fresh

        verification_max_age_seconds = self.verification_max_age_seconds

        members = []
        for members_item_data in self.members:
            members_item = members_item_data.to_dict()
            members.append(members_item)

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        verified_at: str | Unset = UNSET
        if not isinstance(self.verified_at, Unset):
            verified_at = self.verified_at.isoformat()

        last_error_code = self.last_error_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "source_database_id": source_database_id,
                "target_database_id": target_database_id,
                "app_id": app_id,
                "scope": scope,
                "state": state,
                "verification_fresh": verification_fresh,
                "verification_max_age_seconds": verification_max_age_seconds,
                "members": members,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if verified_at is not UNSET:
            field_dict["verified_at"] = verified_at
        if last_error_code is not UNSET:
            field_dict["last_error_code"] = last_error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_cutover_member import ManagedPostgresCutoverMember

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        source_database_id = UUID(d.pop("source_database_id"))

        target_database_id = UUID(d.pop("target_database_id"))

        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        state = check_managed_postgres_cutover_state(d.pop("state"))

        verification_fresh = d.pop("verification_fresh")

        verification_max_age_seconds = d.pop("verification_max_age_seconds")

        members = []
        _members = d.pop("members")
        for members_item_data in _members:
            members_item = ManagedPostgresCutoverMember.from_dict(members_item_data)

            members.append(members_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _verified_at = d.pop("verified_at", UNSET)
        verified_at: datetime.datetime | Unset
        if isinstance(_verified_at, Unset):
            verified_at = UNSET
        else:
            verified_at = datetime.datetime.fromisoformat(_verified_at)

        last_error_code = d.pop("last_error_code", UNSET)

        managed_postgres_cutover = cls(
            id=id,
            source_database_id=source_database_id,
            target_database_id=target_database_id,
            app_id=app_id,
            scope=scope,
            state=state,
            verification_fresh=verification_fresh,
            verification_max_age_seconds=verification_max_age_seconds,
            members=members,
            created_at=created_at,
            updated_at=updated_at,
            verified_at=verified_at,
            last_error_code=last_error_code,
        )

        managed_postgres_cutover.additional_properties = d
        return managed_postgres_cutover

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
