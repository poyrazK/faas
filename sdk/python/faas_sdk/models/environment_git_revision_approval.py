from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.environment_git_reviewed_merge_evidence import EnvironmentGitReviewedMergeEvidence


T = TypeVar("T", bound="EnvironmentGitRevisionApproval")


@_attrs_define
class EnvironmentGitRevisionApproval:
    """Immutable reviewed-merge evidence bound to the approved definition and source generation."""

    id: UUID
    source_id: UUID
    revision_id: UUID
    generation: int
    definition_digest: str
    evidence: EnvironmentGitReviewedMergeEvidence
    """Reviews of the final PR head before merge. Permissions and protection describe the check time, not
    historical policy."""
    recorded_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        source_id = str(self.source_id)

        revision_id = str(self.revision_id)

        generation = self.generation

        definition_digest = self.definition_digest

        evidence = self.evidence.to_dict()

        recorded_at = self.recorded_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "source_id": source_id,
                "revision_id": revision_id,
                "generation": generation,
                "definition_digest": definition_digest,
                "evidence": evidence,
                "recorded_at": recorded_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_git_reviewed_merge_evidence import EnvironmentGitReviewedMergeEvidence

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        source_id = UUID(d.pop("source_id"))

        revision_id = UUID(d.pop("revision_id"))

        generation = d.pop("generation")

        definition_digest = d.pop("definition_digest")

        evidence = EnvironmentGitReviewedMergeEvidence.from_dict(d.pop("evidence"))

        recorded_at = datetime.datetime.fromisoformat(d.pop("recorded_at"))

        environment_git_revision_approval = cls(
            id=id,
            source_id=source_id,
            revision_id=revision_id,
            generation=generation,
            definition_digest=definition_digest,
            evidence=evidence,
            recorded_at=recorded_at,
        )

        environment_git_revision_approval.additional_properties = d
        return environment_git_revision_approval

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
