from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.environment_definition import EnvironmentDefinition


T = TypeVar("T", bound="EnvironmentDesiredRevision")


@_attrs_define
class EnvironmentDesiredRevision:
    """Immutable approved definition retained for reconciliation during Git outages."""

    id: str
    source_id: str
    commit_sha: str
    definition_digest: str
    definition: EnvironmentDefinition
    """Versioned Git intent; omitted fields are unmanaged and removal requires explicit pruning."""
    approved_by: str
    approved_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        source_id = self.source_id

        commit_sha = self.commit_sha

        definition_digest = self.definition_digest

        definition = self.definition.to_dict()

        approved_by = self.approved_by

        approved_at = self.approved_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "source_id": source_id,
                "commit_sha": commit_sha,
                "definition_digest": definition_digest,
                "definition": definition,
                "approved_by": approved_by,
                "approved_at": approved_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_definition import EnvironmentDefinition

        d = dict(src_dict)
        id = d.pop("id")

        source_id = d.pop("source_id")

        commit_sha = d.pop("commit_sha")

        definition_digest = d.pop("definition_digest")

        definition = EnvironmentDefinition.from_dict(d.pop("definition"))

        approved_by = d.pop("approved_by")

        approved_at = datetime.datetime.fromisoformat(d.pop("approved_at"))

        environment_desired_revision = cls(
            id=id,
            source_id=source_id,
            commit_sha=commit_sha,
            definition_digest=definition_digest,
            definition=definition,
            approved_by=approved_by,
            approved_at=approved_at,
        )

        environment_desired_revision.additional_properties = d
        return environment_desired_revision

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
