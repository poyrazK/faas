from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateProjectEnvironmentRequest")


@_attrs_define
class CreateProjectEnvironmentRequest:
    """Request to register or clone a named project environment."""

    slug: str
    """Canonical slug to assign to the new environment; `default` is reserved for application scope."""
    protected: bool | Unset = False
    from_environment: str | Unset = UNSET
    """Source environment whose scoped configuration and values are copied."""
    share_resources: bool | Unset = False
    """Explicitly attach fresh target-scoped credentials to the source environment's managed database and object-
    storage resources; data remains shared."""
    preview_pr_number: int | Unset = UNSET
    """GitHub pull request number for a preview environment."""
    preview_head_sha: str | Unset = UNSET
    """Lowercase full commit SHA for the pull request head; supply it with preview_pr_number."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        protected = self.protected

        from_environment = self.from_environment

        share_resources = self.share_resources

        preview_pr_number = self.preview_pr_number

        preview_head_sha = self.preview_head_sha

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
            }
        )
        if protected is not UNSET:
            field_dict["protected"] = protected
        if from_environment is not UNSET:
            field_dict["from_environment"] = from_environment
        if share_resources is not UNSET:
            field_dict["share_resources"] = share_resources
        if preview_pr_number is not UNSET:
            field_dict["preview_pr_number"] = preview_pr_number
        if preview_head_sha is not UNSET:
            field_dict["preview_head_sha"] = preview_head_sha

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        slug = d.pop("slug")

        protected = d.pop("protected", UNSET)

        from_environment = d.pop("from_environment", UNSET)

        share_resources = d.pop("share_resources", UNSET)

        preview_pr_number = d.pop("preview_pr_number", UNSET)

        preview_head_sha = d.pop("preview_head_sha", UNSET)

        create_project_environment_request = cls(
            slug=slug,
            protected=protected,
            from_environment=from_environment,
            share_resources=share_resources,
            preview_pr_number=preview_pr_number,
            preview_head_sha=preview_head_sha,
        )

        create_project_environment_request.additional_properties = d
        return create_project_environment_request

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
