from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.tool_invocation_outcome_code import ToolInvocationOutcomeCode, check_tool_invocation_outcome_code
from ..models.tool_invocation_outcome_status import ToolInvocationOutcomeStatus, check_tool_invocation_outcome_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="ToolInvocationOutcome")


@_attrs_define
class ToolInvocationOutcome:
    """Worker-reported complete MCP result or explicit preflight/transport uncertainty. Unknown outcomes never authorize a
    replay.

    """

    status: ToolInvocationOutcomeStatus
    code: ToolInvocationOutcomeCode
    result_base64: str | Unset = UNSET
    """Sanitized complete MCP content and structuredContent only; at most 64 KiB decoded"""
    result_sha256: str | Unset = UNSET
    result_bytes: int | Unset = UNSET
    result_omitted: bool | Unset = UNSET
    """True when a complete result exceeded retention size; only its digest and length are retained"""
    redacted: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        code: str = self.code

        result_base64 = self.result_base64

        result_sha256 = self.result_sha256

        result_bytes = self.result_bytes

        result_omitted = self.result_omitted

        redacted = self.redacted

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "code": code,
            }
        )
        if result_base64 is not UNSET:
            field_dict["result_base64"] = result_base64
        if result_sha256 is not UNSET:
            field_dict["result_sha256"] = result_sha256
        if result_bytes is not UNSET:
            field_dict["result_bytes"] = result_bytes
        if result_omitted is not UNSET:
            field_dict["result_omitted"] = result_omitted
        if redacted is not UNSET:
            field_dict["redacted"] = redacted

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_tool_invocation_outcome_status(d.pop("status"))

        code = check_tool_invocation_outcome_code(d.pop("code"))

        result_base64 = d.pop("result_base64", UNSET)

        result_sha256 = d.pop("result_sha256", UNSET)

        result_bytes = d.pop("result_bytes", UNSET)

        result_omitted = d.pop("result_omitted", UNSET)

        redacted = d.pop("redacted", UNSET)

        tool_invocation_outcome = cls(
            status=status,
            code=code,
            result_base64=result_base64,
            result_sha256=result_sha256,
            result_bytes=result_bytes,
            result_omitted=result_omitted,
            redacted=redacted,
        )

        tool_invocation_outcome.additional_properties = d
        return tool_invocation_outcome

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
