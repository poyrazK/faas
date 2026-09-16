from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugReplayComparison")


@_attrs_define
class DebugReplayComparison:
    """Safe metadata-only comparison written to a completed debugger replay invocation."""

    source_deployment_id: None | Unset | UUID = UNSET
    mirror_deployment_id: None | Unset | UUID = UNSET
    source_status_code: int | Unset = UNSET
    mirror_status_code: int | Unset = UNSET
    source_latency_ms: int | Unset = UNSET
    mirror_latency_ms: int | Unset = UNSET
    status_diff: bool | Unset = UNSET
    crashed: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source_deployment_id: None | str | Unset
        if isinstance(self.source_deployment_id, Unset):
            source_deployment_id = UNSET
        elif isinstance(self.source_deployment_id, UUID):
            source_deployment_id = str(self.source_deployment_id)
        else:
            source_deployment_id = self.source_deployment_id

        mirror_deployment_id: None | str | Unset
        if isinstance(self.mirror_deployment_id, Unset):
            mirror_deployment_id = UNSET
        elif isinstance(self.mirror_deployment_id, UUID):
            mirror_deployment_id = str(self.mirror_deployment_id)
        else:
            mirror_deployment_id = self.mirror_deployment_id

        source_status_code = self.source_status_code

        mirror_status_code = self.mirror_status_code

        source_latency_ms = self.source_latency_ms

        mirror_latency_ms = self.mirror_latency_ms

        status_diff = self.status_diff

        crashed = self.crashed

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if source_deployment_id is not UNSET:
            field_dict["source_deployment_id"] = source_deployment_id
        if mirror_deployment_id is not UNSET:
            field_dict["mirror_deployment_id"] = mirror_deployment_id
        if source_status_code is not UNSET:
            field_dict["source_status_code"] = source_status_code
        if mirror_status_code is not UNSET:
            field_dict["mirror_status_code"] = mirror_status_code
        if source_latency_ms is not UNSET:
            field_dict["source_latency_ms"] = source_latency_ms
        if mirror_latency_ms is not UNSET:
            field_dict["mirror_latency_ms"] = mirror_latency_ms
        if status_diff is not UNSET:
            field_dict["status_diff"] = status_diff
        if crashed is not UNSET:
            field_dict["crashed"] = crashed

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

        def _parse_source_deployment_id(data: object) -> None | Unset | UUID:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                source_deployment_id_type_0 = UUID(data)

                return source_deployment_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | Unset | UUID, data)

        source_deployment_id = _parse_source_deployment_id(d.pop("source_deployment_id", UNSET))

        def _parse_mirror_deployment_id(data: object) -> None | Unset | UUID:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                mirror_deployment_id_type_0 = UUID(data)

                return mirror_deployment_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | Unset | UUID, data)

        mirror_deployment_id = _parse_mirror_deployment_id(d.pop("mirror_deployment_id", UNSET))

        source_status_code = d.pop("source_status_code", UNSET)

        mirror_status_code = d.pop("mirror_status_code", UNSET)

        source_latency_ms = d.pop("source_latency_ms", UNSET)

        mirror_latency_ms = d.pop("mirror_latency_ms", UNSET)

        status_diff = d.pop("status_diff", UNSET)

        crashed = d.pop("crashed", UNSET)

        debug_replay_comparison = cls(
            source_deployment_id=source_deployment_id,
            mirror_deployment_id=mirror_deployment_id,
            source_status_code=source_status_code,
            mirror_status_code=mirror_status_code,
            source_latency_ms=source_latency_ms,
            mirror_latency_ms=mirror_latency_ms,
            status_diff=status_diff,
            crashed=crashed,
        )

        debug_replay_comparison.additional_properties = d
        return debug_replay_comparison

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
