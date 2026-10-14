from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="SourceReviewRequest")


@_attrs_define
class SourceReviewRequest:
    """Git repository and revision selectors to resolve without creating a deployment."""

    repo: str
    ref: str
    """GitHub branch or full lowercase commit SHA; use the commit SHA for tags."""
    environment: str | Unset = UNSET
    """Registered target environment; omitted selects default."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        repo = self.repo

        ref = self.ref

        environment = self.environment

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "repo": repo,
                "ref": ref,
            }
        )
        if environment is not UNSET:
            field_dict["environment"] = environment

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        repo = d.pop("repo")

        ref = d.pop("ref")

        environment = d.pop("environment", UNSET)

        source_review_request = cls(
            repo=repo,
            ref=ref,
            environment=environment,
        )

        source_review_request.additional_properties = d
        return source_review_request

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
