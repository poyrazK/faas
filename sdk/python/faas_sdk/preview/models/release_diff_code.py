from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ReleaseDiffCode")


@_attrs_define
class ReleaseDiffCode:
    """How the releases' source and image differ."""

    changed: bool
    image_changed: bool
    from_commit: str | Unset = UNSET
    to_commit: str | Unset = UNSET
    compare_url: str | Unset = UNSET
    """GitHub's compare page when both releases came from the same GitHub repository."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        changed = self.changed

        image_changed = self.image_changed

        from_commit = self.from_commit

        to_commit = self.to_commit

        compare_url = self.compare_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "changed": changed,
                "image_changed": image_changed,
            }
        )
        if from_commit is not UNSET:
            field_dict["from_commit"] = from_commit
        if to_commit is not UNSET:
            field_dict["to_commit"] = to_commit
        if compare_url is not UNSET:
            field_dict["compare_url"] = compare_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        changed = d.pop("changed")

        image_changed = d.pop("image_changed")

        from_commit = d.pop("from_commit", UNSET)

        to_commit = d.pop("to_commit", UNSET)

        compare_url = d.pop("compare_url", UNSET)

        release_diff_code = cls(
            changed=changed,
            image_changed=image_changed,
            from_commit=from_commit,
            to_commit=to_commit,
            compare_url=compare_url,
        )

        release_diff_code.additional_properties = d
        return release_diff_code

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
