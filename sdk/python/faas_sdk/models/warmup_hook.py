from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="WarmupHook")


@_attrs_define
class WarmupHook:
    """Path that primes a deployment's first init snapshot. Before that capture Gregale sends up to 20 GET requests (5 s at
    most) to it on the app's HTTP port; without it, the HTTP health check path is used. Priming never fails a deploy.

    """

    path: str | Unset = UNSET
    """ Absolute path on the app's HTTP port. Should exercise the request code a wake needs warm and be safe to call
    repeatedly. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if path is not UNSET:
            field_dict["path"] = path

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        path = d.pop("path", UNSET)

        warmup_hook = cls(
            path=path,
        )

        warmup_hook.additional_properties = d
        return warmup_hook

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
