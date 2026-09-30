from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.environment_git_source_spec import EnvironmentGitSourceSpec


T = TypeVar("T", bound="EnvironmentGitSource")


@_attrs_define
class EnvironmentGitSource:
    """Durable environment authority with separate approved and fully applied revision pointers."""

    id: str
    account_id: str
    project_id: str
    environment_id: str
    environment: str
    source: EnvironmentGitSourceSpec
    """Pinned repository identity and source-of-truth controls for one environment."""
    suspended: bool
    generation: int
    intent_version: int
    created_at: datetime.datetime
    updated_at: datetime.datetime
    approved_revision_id: str | Unset = UNSET
    applied_revision_id: str | Unset = UNSET
    source_checked_at: datetime.datetime | Unset = UNSET
    source_error_code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        account_id = self.account_id

        project_id = self.project_id

        environment_id = self.environment_id

        environment = self.environment

        source = self.source.to_dict()

        suspended = self.suspended

        generation = self.generation

        intent_version = self.intent_version

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        approved_revision_id = self.approved_revision_id

        applied_revision_id = self.applied_revision_id

        source_checked_at: str | Unset = UNSET
        if not isinstance(self.source_checked_at, Unset):
            source_checked_at = self.source_checked_at.isoformat()

        source_error_code = self.source_error_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "account_id": account_id,
                "project_id": project_id,
                "environment_id": environment_id,
                "environment": environment,
                "source": source,
                "suspended": suspended,
                "generation": generation,
                "intent_version": intent_version,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if approved_revision_id is not UNSET:
            field_dict["approved_revision_id"] = approved_revision_id
        if applied_revision_id is not UNSET:
            field_dict["applied_revision_id"] = applied_revision_id
        if source_checked_at is not UNSET:
            field_dict["source_checked_at"] = source_checked_at
        if source_error_code is not UNSET:
            field_dict["source_error_code"] = source_error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_git_source_spec import EnvironmentGitSourceSpec

        d = dict(src_dict)
        id = d.pop("id")

        account_id = d.pop("account_id")

        project_id = d.pop("project_id")

        environment_id = d.pop("environment_id")

        environment = d.pop("environment")

        source = EnvironmentGitSourceSpec.from_dict(d.pop("source"))

        suspended = d.pop("suspended")

        generation = d.pop("generation")

        intent_version = d.pop("intent_version")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        approved_revision_id = d.pop("approved_revision_id", UNSET)

        applied_revision_id = d.pop("applied_revision_id", UNSET)

        _source_checked_at = d.pop("source_checked_at", UNSET)
        source_checked_at: datetime.datetime | Unset
        if isinstance(_source_checked_at, Unset):
            source_checked_at = UNSET
        else:
            source_checked_at = datetime.datetime.fromisoformat(_source_checked_at)

        source_error_code = d.pop("source_error_code", UNSET)

        environment_git_source = cls(
            id=id,
            account_id=account_id,
            project_id=project_id,
            environment_id=environment_id,
            environment=environment,
            source=source,
            suspended=suspended,
            generation=generation,
            intent_version=intent_version,
            created_at=created_at,
            updated_at=updated_at,
            approved_revision_id=approved_revision_id,
            applied_revision_id=applied_revision_id,
            source_checked_at=source_checked_at,
            source_error_code=source_error_code,
        )

        environment_git_source.additional_properties = d
        return environment_git_source

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
