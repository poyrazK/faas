from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.mirror_replay_invocation import MirrorReplayInvocation


T = TypeVar("T", bound="MirrorReplayBatchResponse")


@_attrs_define
class MirrorReplayBatchResponse:
    """The replay invocations accepted and queued for asynchronous mirror execution."""

    queued: int
    invocations: list[MirrorReplayInvocation]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        queued = self.queued

        invocations = []
        for invocations_item_data in self.invocations:
            invocations_item = invocations_item_data.to_dict()
            invocations.append(invocations_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "queued": queued,
                "invocations": invocations,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mirror_replay_invocation import MirrorReplayInvocation

        d = dict(src_dict)
        queued = d.pop("queued")

        invocations = []
        _invocations = d.pop("invocations")
        for invocations_item_data in _invocations:
            invocations_item = MirrorReplayInvocation.from_dict(invocations_item_data)

            invocations.append(invocations_item)

        mirror_replay_batch_response = cls(
            queued=queued,
            invocations=invocations,
        )

        mirror_replay_batch_response.additional_properties = d
        return mirror_replay_batch_response

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
