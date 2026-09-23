from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mirror_replay_invocation_status import MirrorReplayInvocationStatus, check_mirror_replay_invocation_status

T = TypeVar("T", bound="MirrorReplayInvocation")


@_attrs_define
class MirrorReplayInvocation:
    """Correlation metadata for one queued mirror replay invocation."""

    request_id: str
    mirror_invocation_id: UUID
    status: MirrorReplayInvocationStatus
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = self.request_id

        mirror_invocation_id = str(self.mirror_invocation_id)

        status: str = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_id": request_id,
                "mirror_invocation_id": mirror_invocation_id,
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = d.pop("request_id")

        mirror_invocation_id = UUID(d.pop("mirror_invocation_id"))

        status = check_mirror_replay_invocation_status(d.pop("status"))

        mirror_replay_invocation = cls(
            request_id=request_id,
            mirror_invocation_id=mirror_invocation_id,
            status=status,
        )

        mirror_replay_invocation.additional_properties = d
        return mirror_replay_invocation

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
