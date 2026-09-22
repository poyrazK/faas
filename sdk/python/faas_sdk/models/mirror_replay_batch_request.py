from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mirror_replay_request_item import MirrorReplayRequestItem


T = TypeVar("T", bound="MirrorReplayBatchRequest")


@_attrs_define
class MirrorReplayBatchRequest:
    requests: list[MirrorReplayRequestItem]
    allow_unsafe_methods: bool | Unset = False
    """Explicit acknowledgement required when any item uses POST, PUT, PATCH, or DELETE."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        requests = []
        for requests_item_data in self.requests:
            requests_item = requests_item_data.to_dict()
            requests.append(requests_item)

        allow_unsafe_methods = self.allow_unsafe_methods

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "requests": requests,
            }
        )
        if allow_unsafe_methods is not UNSET:
            field_dict["allow_unsafe_methods"] = allow_unsafe_methods

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mirror_replay_request_item import MirrorReplayRequestItem

        d = dict(src_dict)
        requests = []
        _requests = d.pop("requests")
        for requests_item_data in _requests:
            requests_item = MirrorReplayRequestItem.from_dict(requests_item_data)

            requests.append(requests_item)

        allow_unsafe_methods = d.pop("allow_unsafe_methods", UNSET)

        mirror_replay_batch_request = cls(
            requests=requests,
            allow_unsafe_methods=allow_unsafe_methods,
        )

        mirror_replay_batch_request.additional_properties = d
        return mirror_replay_batch_request

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
