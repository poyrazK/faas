from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.upsert_dev_session_request_runtime import (
    UpsertDevSessionRequestRuntime,
    check_upsert_dev_session_request_runtime,
)
from ..models.upsert_dev_session_request_type import UpsertDevSessionRequestType, check_upsert_dev_session_request_type
from ..types import UNSET, Unset

T = TypeVar("T", bound="UpsertDevSessionRequest")


@_attrs_define
class UpsertDevSessionRequest:
    """Application shape and opaque local identity for a CLI-managed developer environment."""

    type_: UpsertDevSessionRequestType | Unset = "app"
    runtime: UpsertDevSessionRequestRuntime | Unset = UNSET
    workspace_id: str | Unset = UNSET
    """Opaque identity derived locally from the CLI installation and canonical source path. Omit only for legacy
    sessions."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_: str | Unset = UNSET
        if not isinstance(self.type_, Unset):
            type_ = self.type_

        runtime: str | Unset = UNSET
        if not isinstance(self.runtime, Unset):
            runtime = self.runtime

        workspace_id = self.workspace_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if type_ is not UNSET:
            field_dict["type"] = type_
        if runtime is not UNSET:
            field_dict["runtime"] = runtime
        if workspace_id is not UNSET:
            field_dict["workspace_id"] = workspace_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _type_ = d.pop("type", UNSET)
        type_: UpsertDevSessionRequestType | Unset
        if isinstance(_type_, Unset):
            type_ = UNSET
        else:
            type_ = check_upsert_dev_session_request_type(_type_)

        _runtime = d.pop("runtime", UNSET)
        runtime: UpsertDevSessionRequestRuntime | Unset
        if isinstance(_runtime, Unset):
            runtime = UNSET
        else:
            runtime = check_upsert_dev_session_request_runtime(_runtime)

        workspace_id = d.pop("workspace_id", UNSET)

        upsert_dev_session_request = cls(
            type_=type_,
            runtime=runtime,
            workspace_id=workspace_id,
        )

        upsert_dev_session_request.additional_properties = d
        return upsert_dev_session_request

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
