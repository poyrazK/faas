from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.config_guard import ConfigGuard
    from ..models.config_revision import ConfigRevision


T = TypeVar("T", bound="ListConfigRevisionsResponse")


@_attrs_define
class ListConfigRevisionsResponse:
    """A scope's config revisions, newest first, and its newest config guard."""

    revisions: list[ConfigRevision]
    guard: ConfigGuard | Unset = UNSET
    """A scope's config guard."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        revisions = []
        for revisions_item_data in self.revisions:
            revisions_item = revisions_item_data.to_dict()
            revisions.append(revisions_item)

        guard: dict[str, Any] | Unset = UNSET
        if not isinstance(self.guard, Unset):
            guard = self.guard.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "revisions": revisions,
            }
        )
        if guard is not UNSET:
            field_dict["guard"] = guard

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.config_guard import ConfigGuard
        from ..models.config_revision import ConfigRevision

        d = dict(src_dict)
        revisions = []
        _revisions = d.pop("revisions")
        for revisions_item_data in _revisions:
            revisions_item = ConfigRevision.from_dict(revisions_item_data)

            revisions.append(revisions_item)

        _guard = d.pop("guard", UNSET)
        guard: ConfigGuard | Unset
        if isinstance(_guard, Unset):
            guard = UNSET
        else:
            guard = ConfigGuard.from_dict(_guard)

        list_config_revisions_response = cls(
            revisions=revisions,
            guard=guard,
        )

        list_config_revisions_response.additional_properties = d
        return list_config_revisions_response

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
