from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_replay_response_status import DebugReplayResponseStatus, check_debug_replay_response_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugReplayResponse")


@_attrs_define
class DebugReplayResponse:
    """POST response from /v1/apps/{slug}/debug/requests/{req_id}/replay (ADR-127)."""

    status: DebugReplayResponseStatus
    mirror_invocation_id: None | Unset | UUID = UNSET
    """Durable invocation ID for polling replay status and comparison results."""
    source_deployment_id: None | Unset | UUID = UNSET
    """Deployment that served the retained request."""
    mirror_deployment_id: None | Unset | UUID = UNSET
    """Enabled mirror target selected for this replay."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        mirror_invocation_id: None | str | Unset
        if isinstance(self.mirror_invocation_id, Unset):
            mirror_invocation_id = UNSET
        elif isinstance(self.mirror_invocation_id, UUID):
            mirror_invocation_id = str(self.mirror_invocation_id)
        else:
            mirror_invocation_id = self.mirror_invocation_id

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

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
            }
        )
        if mirror_invocation_id is not UNSET:
            field_dict["mirror_invocation_id"] = mirror_invocation_id
        if source_deployment_id is not UNSET:
            field_dict["source_deployment_id"] = source_deployment_id
        if mirror_deployment_id is not UNSET:
            field_dict["mirror_deployment_id"] = mirror_deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_debug_replay_response_status(d.pop("status"))

        def _parse_mirror_invocation_id(data: object) -> None | Unset | UUID:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                mirror_invocation_id_type_0 = UUID(data)

                return mirror_invocation_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | Unset | UUID, data)

        mirror_invocation_id = _parse_mirror_invocation_id(d.pop("mirror_invocation_id", UNSET))

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

        debug_replay_response = cls(
            status=status,
            mirror_invocation_id=mirror_invocation_id,
            source_deployment_id=source_deployment_id,
            mirror_deployment_id=mirror_deployment_id,
        )

        debug_replay_response.additional_properties = d
        return debug_replay_response

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
