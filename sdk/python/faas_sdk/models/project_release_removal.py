from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ProjectReleaseRemoval")


@_attrs_define
class ProjectReleaseRemoval:
    """A serving service retired at cutover (ADR-20261021120000). The fingerprint binds its settings at planning."""

    app_id: str
    slug: str
    root_dir: str
    fingerprint: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        slug = self.slug

        root_dir = self.root_dir

        fingerprint = self.fingerprint

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "slug": slug,
                "root_dir": root_dir,
                "fingerprint": fingerprint,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        slug = d.pop("slug")

        root_dir = d.pop("root_dir")

        fingerprint = d.pop("fingerprint")

        project_release_removal = cls(
            app_id=app_id,
            slug=slug,
            root_dir=root_dir,
            fingerprint=fingerprint,
        )

        project_release_removal.additional_properties = d
        return project_release_removal

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
