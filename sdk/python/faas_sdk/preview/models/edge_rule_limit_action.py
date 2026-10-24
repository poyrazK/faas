from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleLimitAction")


@_attrs_define
class EdgeRuleLimitAction:
    """Standalone per-route body-size cap (ADR-091 D24 / §4.1.2.13).
    The primitive for "POST /upload ≤ 5 MB, POST /users ≤ 1 MB,
    POST /webhooks ≤ 2 MB" without shipping a JSON Schema. The
    hot-path applier (§4.1.2.8c) installs `http.MaxBytesReader`
    on the inbound body and short-circuits oversize requests
    with 413 `request_too_large` — and, more importantly,
    performs a Content-Length fast-path deny so a 30 MB body on
    a 5 MB cap costs zero bytes of buffering (a `MaxBytesReader`
    alone only trips when something reads the body, and on this
    hot path nothing reads it until the proxy leg).

    Rejections never reach the wake gate, the auth chain, or the
    rate limiter — same posture as kind=validate. Free-and-above
    (no plan gate).

    Field-by-field:
      * `max_body_bytes` — required buffered-path cap. Must be
        > 0 and ≤ `MaxRequestBodyBytes` (25 MiB). A standalone
        limit rule with no cap is a silent no-op and is
        rejected at create-time with 422 — use `kind=validate`
        if you need a body cap alongside a JSON Schema.
      * `max_body_bytes_streaming` — optional streaming opt-in
        cap (≤ `MaxEdgeRuleLimitBodyBytesStreaming` = 100 MiB).
        0 (default) = no streaming carve-out; the buffered
        `max_body_bytes` is the cap on both paths. When set,
        must be ≥ `max_body_bytes` — a streaming cap tighter
        than the buffered cap would 413 every streaming request
        for a body already accepted as buffered. Runtime
        enforcement of this field is declared + clamped at
        create-time but deferred at the §4.1.2.8c slot to a
        follow-up PR (stated in ADR-091 D24 §6).

    """

    max_body_bytes: int
    """Required per-rule buffered-path body cap. Must be > 0
    and ≤ `api.MaxRequestBodyBytes` (25 MiB). A standalone
    limit rule with no cap is rejected at create-time with
    422 — use `kind=validate` if you need a body cap
    alongside a JSON Schema.
    """
    max_body_bytes_streaming: int | Unset = UNSET
    """Optional streaming opt-in cap. 0 (default) = no
    streaming carve-out; the buffered `max_body_bytes` is
    the cap on both paths. Must be ≥ `max_body_bytes` when
    set. Runtime enforcement deferred to follow-up PR
    (ADR-091 D24 §6).
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        max_body_bytes = self.max_body_bytes

        max_body_bytes_streaming = self.max_body_bytes_streaming

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "max_body_bytes": max_body_bytes,
            }
        )
        if max_body_bytes_streaming is not UNSET:
            field_dict["max_body_bytes_streaming"] = max_body_bytes_streaming

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_body_bytes = d.pop("max_body_bytes")

        max_body_bytes_streaming = d.pop("max_body_bytes_streaming", UNSET)

        edge_rule_limit_action = cls(
            max_body_bytes=max_body_bytes,
            max_body_bytes_streaming=max_body_bytes_streaming,
        )

        edge_rule_limit_action.additional_properties = d
        return edge_rule_limit_action

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
