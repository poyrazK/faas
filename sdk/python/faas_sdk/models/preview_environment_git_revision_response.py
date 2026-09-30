from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.environment_definition import EnvironmentDefinition


T = TypeVar("T", bound="PreviewEnvironmentGitRevisionResponse")


@_attrs_define
class PreviewEnvironmentGitRevisionResponse:
    """Verified Git bytes with the digest and source generation needed for approval."""

    commit_sha: str
    definition_digest: str
    definition: EnvironmentDefinition
    """Versioned Git intent; omitted fields are unmanaged and removal requires explicit pruning."""
    generation: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        commit_sha = self.commit_sha

        definition_digest = self.definition_digest

        definition = self.definition.to_dict()

        generation = self.generation

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "commit_sha": commit_sha,
                "definition_digest": definition_digest,
                "definition": definition,
                "generation": generation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_definition import EnvironmentDefinition

        d = dict(src_dict)
        commit_sha = d.pop("commit_sha")

        definition_digest = d.pop("definition_digest")

        definition = EnvironmentDefinition.from_dict(d.pop("definition"))

        generation = d.pop("generation")

        preview_environment_git_revision_response = cls(
            commit_sha=commit_sha,
            definition_digest=definition_digest,
            definition=definition,
            generation=generation,
        )

        preview_environment_git_revision_response.additional_properties = d
        return preview_environment_git_revision_response

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
