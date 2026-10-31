from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mirror_replay_request_item_method import (
    MirrorReplayRequestItemMethod,
    check_mirror_replay_request_item_method,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mirror_replay_request_item_headers import MirrorReplayRequestItemHeaders


T = TypeVar("T", bound="MirrorReplayRequestItem")


@_attrs_define
class MirrorReplayRequestItem:
    """One sanitized historical request and its optional expected source-response metadata."""

    method: MirrorReplayRequestItemMethod
    path: str
    """Absolute request path with optional query; absolute URLs are rejected."""
    request_id: str | Unset = UNSET
    """Optional correlation id; generated when omitted."""
    headers: MirrorReplayRequestItemHeaders | Unset = UNSET
    body: Any | Unset = UNSET
    """Sanitized JSON request body, bounded to 64 KiB."""
    expected_status: int | Unset = UNSET
    expected_latency_ms: int | Unset = UNSET
    expected_body_sha256: str | Unset = UNSET
    """Optional SHA-256 of the historical source response body. The body itself is not uploaded."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method: str = self.method

        path = self.path

        request_id = self.request_id

        headers: dict[str, Any] | Unset = UNSET
        if not isinstance(self.headers, Unset):
            headers = self.headers.to_dict()

        body = self.body

        expected_status = self.expected_status

        expected_latency_ms = self.expected_latency_ms

        expected_body_sha256 = self.expected_body_sha256

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "path": path,
            }
        )
        if request_id is not UNSET:
            field_dict["request_id"] = request_id
        if headers is not UNSET:
            field_dict["headers"] = headers
        if body is not UNSET:
            field_dict["body"] = body
        if expected_status is not UNSET:
            field_dict["expected_status"] = expected_status
        if expected_latency_ms is not UNSET:
            field_dict["expected_latency_ms"] = expected_latency_ms
        if expected_body_sha256 is not UNSET:
            field_dict["expected_body_sha256"] = expected_body_sha256

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mirror_replay_request_item_headers import MirrorReplayRequestItemHeaders

        d = dict(src_dict)
        method = check_mirror_replay_request_item_method(d.pop("method"))

        path = d.pop("path")

        request_id = d.pop("request_id", UNSET)

        _headers = d.pop("headers", UNSET)
        headers: MirrorReplayRequestItemHeaders | Unset
        if isinstance(_headers, Unset):
            headers = UNSET
        else:
            headers = MirrorReplayRequestItemHeaders.from_dict(_headers)

        body = d.pop("body", UNSET)

        expected_status = d.pop("expected_status", UNSET)

        expected_latency_ms = d.pop("expected_latency_ms", UNSET)

        expected_body_sha256 = d.pop("expected_body_sha256", UNSET)

        mirror_replay_request_item = cls(
            method=method,
            path=path,
            request_id=request_id,
            headers=headers,
            body=body,
            expected_status=expected_status,
            expected_latency_ms=expected_latency_ms,
            expected_body_sha256=expected_body_sha256,
        )

        mirror_replay_request_item.additional_properties = d
        return mirror_replay_request_item

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
