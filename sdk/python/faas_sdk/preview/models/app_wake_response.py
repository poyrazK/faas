from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AppWakeResponse")


@_attrs_define
class AppWakeResponse:
    """Correlation handle for an accepted explicit app wake."""

    wake_id: UUID
    """Wake id on the admitted instance and wake timeline (the running instance's when already_running)."""
    already_running: bool | Unset = UNSET
    """True when the app already had a routable running instance and no wake was queued."""
    instance_id: UUID | Unset = UNSET
    """The running instance, present when already_running is true."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        wake_id = str(self.wake_id)

        already_running = self.already_running

        instance_id: str | Unset = UNSET
        if not isinstance(self.instance_id, Unset):
            instance_id = str(self.instance_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "wake_id": wake_id,
            }
        )
        if already_running is not UNSET:
            field_dict["already_running"] = already_running
        if instance_id is not UNSET:
            field_dict["instance_id"] = instance_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        wake_id = UUID(d.pop("wake_id"))

        already_running = d.pop("already_running", UNSET)

        _instance_id = d.pop("instance_id", UNSET)
        instance_id: UUID | Unset
        if isinstance(_instance_id, Unset):
            instance_id = UNSET
        else:
            instance_id = UUID(_instance_id)

        app_wake_response = cls(
            wake_id=wake_id,
            already_running=already_running,
            instance_id=instance_id,
        )

        app_wake_response.additional_properties = d
        return app_wake_response

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
