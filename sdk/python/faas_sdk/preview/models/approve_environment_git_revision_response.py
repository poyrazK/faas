from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.environment_desired_revision import EnvironmentDesiredRevision
    from ..models.environment_git_source import EnvironmentGitSource


T = TypeVar("T", bound="ApproveEnvironmentGitRevisionResponse")


@_attrs_define
class ApproveEnvironmentGitRevisionResponse:
    """Durable approved revision and the updated management source."""

    source: EnvironmentGitSource
    """Durable environment authority with separate approved and fully applied revision pointers."""
    revision: EnvironmentDesiredRevision
    """Immutable approved definition retained for reconciliation during Git outages."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source = self.source.to_dict()

        revision = self.revision.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "revision": revision,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_desired_revision import EnvironmentDesiredRevision
        from ..models.environment_git_source import EnvironmentGitSource

        d = dict(src_dict)
        source = EnvironmentGitSource.from_dict(d.pop("source"))

        revision = EnvironmentDesiredRevision.from_dict(d.pop("revision"))

        approve_environment_git_revision_response = cls(
            source=source,
            revision=revision,
        )

        approve_environment_git_revision_response.additional_properties = d
        return approve_environment_git_revision_response

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
