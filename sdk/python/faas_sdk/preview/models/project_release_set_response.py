from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_release_deactivation_receipt import ProjectReleaseDeactivationReceipt
    from ..models.project_release_set_member_response import ProjectReleaseSetMemberResponse


T = TypeVar("T", bound="ProjectReleaseSetResponse")


@_attrs_define
class ProjectReleaseSetResponse:
    """Immutable project deployment graph. Active sets do not expire; when replaced, their TTL starts and expires_at is
    set.

    """

    id: UUID
    account_id: UUID
    project_id: UUID
    environment: str
    active: bool
    ttl_seconds: int
    created_at: datetime.datetime
    members: list[ProjectReleaseSetMemberResponse]
    deactivation: ProjectReleaseDeactivationReceipt | Unset = UNSET
    """Recorded deactivation decision and exact fallback routes reviewed for a project graph."""
    rollback_source_release_id: UUID | Unset = UNSET
    rollback_previous_release_id: UUID | Unset = UNSET
    expires_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        account_id = str(self.account_id)

        project_id = str(self.project_id)

        environment = self.environment

        active = self.active

        ttl_seconds = self.ttl_seconds

        created_at = self.created_at.isoformat()

        members = []
        for members_item_data in self.members:
            members_item = members_item_data.to_dict()
            members.append(members_item)

        deactivation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.deactivation, Unset):
            deactivation = self.deactivation.to_dict()

        rollback_source_release_id: str | Unset = UNSET
        if not isinstance(self.rollback_source_release_id, Unset):
            rollback_source_release_id = str(self.rollback_source_release_id)

        rollback_previous_release_id: str | Unset = UNSET
        if not isinstance(self.rollback_previous_release_id, Unset):
            rollback_previous_release_id = str(self.rollback_previous_release_id)

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "account_id": account_id,
                "project_id": project_id,
                "environment": environment,
                "active": active,
                "ttl_seconds": ttl_seconds,
                "created_at": created_at,
                "members": members,
            }
        )
        if deactivation is not UNSET:
            field_dict["deactivation"] = deactivation
        if rollback_source_release_id is not UNSET:
            field_dict["rollback_source_release_id"] = rollback_source_release_id
        if rollback_previous_release_id is not UNSET:
            field_dict["rollback_previous_release_id"] = rollback_previous_release_id
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_release_deactivation_receipt import ProjectReleaseDeactivationReceipt
        from ..models.project_release_set_member_response import ProjectReleaseSetMemberResponse

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        account_id = UUID(d.pop("account_id"))

        project_id = UUID(d.pop("project_id"))

        environment = d.pop("environment")

        active = d.pop("active")

        ttl_seconds = d.pop("ttl_seconds")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        members = []
        _members = d.pop("members")
        for members_item_data in _members:
            members_item = ProjectReleaseSetMemberResponse.from_dict(members_item_data)

            members.append(members_item)

        _deactivation = d.pop("deactivation", UNSET)
        deactivation: ProjectReleaseDeactivationReceipt | Unset
        if isinstance(_deactivation, Unset):
            deactivation = UNSET
        else:
            deactivation = ProjectReleaseDeactivationReceipt.from_dict(_deactivation)

        _rollback_source_release_id = d.pop("rollback_source_release_id", UNSET)
        rollback_source_release_id: UUID | Unset
        if isinstance(_rollback_source_release_id, Unset):
            rollback_source_release_id = UNSET
        else:
            rollback_source_release_id = UUID(_rollback_source_release_id)

        _rollback_previous_release_id = d.pop("rollback_previous_release_id", UNSET)
        rollback_previous_release_id: UUID | Unset
        if isinstance(_rollback_previous_release_id, Unset):
            rollback_previous_release_id = UNSET
        else:
            rollback_previous_release_id = UUID(_rollback_previous_release_id)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        project_release_set_response = cls(
            id=id,
            account_id=account_id,
            project_id=project_id,
            environment=environment,
            active=active,
            ttl_seconds=ttl_seconds,
            created_at=created_at,
            members=members,
            deactivation=deactivation,
            rollback_source_release_id=rollback_source_release_id,
            rollback_previous_release_id=rollback_previous_release_id,
            expires_at=expires_at,
        )

        project_release_set_response.additional_properties = d
        return project_release_set_response

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
