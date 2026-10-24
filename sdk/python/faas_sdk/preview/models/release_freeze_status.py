from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.release_freeze import ReleaseFreeze


T = TypeVar("T", bound="ReleaseFreezeStatus")


@_attrs_define
class ReleaseFreezeStatus:
    """Whether a production release of the app would be refused now."""

    app: str
    frozen: bool
    active: list[ReleaseFreeze]
    """The freezes refusing a production release now."""
    next_: ReleaseFreeze | Unset = UNSET
    """A deploy freeze on production releases of one app or, without `app`, of every app of the account
    (ADR-20261019071733)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        frozen = self.frozen

        active = []
        for active_item_data in self.active:
            active_item = active_item_data.to_dict()
            active.append(active_item)

        next_: dict[str, Any] | Unset = UNSET
        if not isinstance(self.next_, Unset):
            next_ = self.next_.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "frozen": frozen,
                "active": active,
            }
        )
        if next_ is not UNSET:
            field_dict["next"] = next_

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.release_freeze import ReleaseFreeze

        d = dict(src_dict)
        app = d.pop("app")

        frozen = d.pop("frozen")

        active = []
        _active = d.pop("active")
        for active_item_data in _active:
            active_item = ReleaseFreeze.from_dict(active_item_data)

            active.append(active_item)

        _next_ = d.pop("next", UNSET)
        next_: ReleaseFreeze | Unset
        if isinstance(_next_, Unset):
            next_ = UNSET
        else:
            next_ = ReleaseFreeze.from_dict(_next_)

        release_freeze_status = cls(
            app=app,
            frozen=frozen,
            active=active,
            next_=next_,
        )

        release_freeze_status.additional_properties = d
        return release_freeze_status

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
