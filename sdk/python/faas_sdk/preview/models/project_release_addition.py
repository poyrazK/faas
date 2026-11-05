from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectReleaseAddition")


@_attrs_define
class ProjectReleaseAddition:
    """Source-owned settings bound by the plan. app_id is assigned during durable reservation."""

    slug: str
    root_dir: str
    fingerprint: str
    app_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        root_dir = self.root_dir

        fingerprint = self.fingerprint

        app_id = self.app_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "root_dir": root_dir,
                "fingerprint": fingerprint,
            }
        )
        if app_id is not UNSET:
            field_dict["app_id"] = app_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        slug = d.pop("slug")

        root_dir = d.pop("root_dir")

        fingerprint = d.pop("fingerprint")

        app_id = d.pop("app_id", UNSET)

        project_release_addition = cls(
            slug=slug,
            root_dir=root_dir,
            fingerprint=fingerprint,
            app_id=app_id,
        )

        project_release_addition.additional_properties = d
        return project_release_addition

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
