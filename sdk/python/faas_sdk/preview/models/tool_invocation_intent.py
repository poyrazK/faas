from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.tool_invocation_intent_access_mode import (
    ToolInvocationIntentAccessMode,
    check_tool_invocation_intent_access_mode,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ToolInvocationIntent")


@_attrs_define
class ToolInvocationIntent:
    """Explicit one-call consent with a canonical argument digest and optional opaque credential binding; no argument
    bodies or credentials.

    """

    version: str
    name: str
    arguments_sha256: str
    access_mode: ToolInvocationIntentAccessMode
    timeout_seconds: int
    allow_tool_execution: bool
    credential_binding_sha256: str | Unset = UNSET
    """Local token-variable hash or exact owner-granted private binding; never a token hash or value"""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        name = self.name

        arguments_sha256 = self.arguments_sha256

        access_mode: str = self.access_mode

        timeout_seconds = self.timeout_seconds

        allow_tool_execution = self.allow_tool_execution

        credential_binding_sha256 = self.credential_binding_sha256

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "name": name,
                "arguments_sha256": arguments_sha256,
                "access_mode": access_mode,
                "timeout_seconds": timeout_seconds,
                "allow_tool_execution": allow_tool_execution,
            }
        )
        if credential_binding_sha256 is not UNSET:
            field_dict["credential_binding_sha256"] = credential_binding_sha256

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = d.pop("version")

        name = d.pop("name")

        arguments_sha256 = d.pop("arguments_sha256")

        access_mode = check_tool_invocation_intent_access_mode(d.pop("access_mode"))

        timeout_seconds = d.pop("timeout_seconds")

        allow_tool_execution = d.pop("allow_tool_execution")

        credential_binding_sha256 = d.pop("credential_binding_sha256", UNSET)

        tool_invocation_intent = cls(
            version=version,
            name=name,
            arguments_sha256=arguments_sha256,
            access_mode=access_mode,
            timeout_seconds=timeout_seconds,
            allow_tool_execution=allow_tool_execution,
            credential_binding_sha256=credential_binding_sha256,
        )

        tool_invocation_intent.additional_properties = d
        return tool_invocation_intent

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
