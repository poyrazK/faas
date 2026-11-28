from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.label_change_set import LabelChangeSet


T = TypeVar("T", bound="LabelChange")


@_attrs_define
class LabelChange:
    """Labels to set and keys to remove, applied together. Setting a key to its current value and removing an absent key
    change nothing. A key may not be both set and removed, and keys under gregale.dev/ cannot be changed.

        Example:
            {'set': {'team': 'billing'}, 'remove': ['tier']}

    """

    set_: LabelChangeSet | Unset = UNSET
    """Keys to set and their values."""
    remove: list[str] | Unset = UNSET
    """Keys to remove."""

    def to_dict(self) -> dict[str, Any]:
        set_: dict[str, Any] | Unset = UNSET
        if not isinstance(self.set_, Unset):
            set_ = self.set_.to_dict()

        remove: list[str] | Unset = UNSET
        if not isinstance(self.remove, Unset):
            remove = self.remove

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if set_ is not UNSET:
            field_dict["set"] = set_
        if remove is not UNSET:
            field_dict["remove"] = remove

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.label_change_set import LabelChangeSet

        d = dict(src_dict)
        _set_ = d.pop("set", UNSET)
        set_: LabelChangeSet | Unset
        if isinstance(_set_, Unset):
            set_ = UNSET
        else:
            set_ = LabelChangeSet.from_dict(_set_)

        remove = cast(list[str], d.pop("remove", UNSET))

        label_change = cls(
            set_=set_,
            remove=remove,
        )

        return label_change
