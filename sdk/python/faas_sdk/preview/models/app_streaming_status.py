from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_streaming_status_cap_kind import AppStreamingStatusCapKind, check_app_streaming_status_cap_kind
from ..models.app_streaming_status_status import AppStreamingStatusStatus, check_app_streaming_status_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="AppStreamingStatus")


@_attrs_define
class AppStreamingStatus:
    """Per-app streaming classification (ADR-102 D6). The probe
    returns the same `Streaming-Status` enum value the
    gatewayd handler would stamp on the response header for a
    representative request to this app, plus the effective
    response-body cap and the per-gate flags that produced it.

    `status` values (closed enum, see `pkg/api/limits.go`):

      - `streaming` — request will stream. `effective_cap_bytes`
        is the cap `capWriter` will enforce.
      - `accept-json-downgrade` — pre-D3 customers with
        `Accept: application/json` would have been buffered.
        Post-D3 (this PR), the request streams regardless of
        Accept; the enum variant survives one cycle so pinned
        SDKs can grep for it.
      - `flag-disabled` — `streaming_enabled=false` on the app.
      - `plan-disallows` — plan tier forbids `streaming_enabled=true`;
        CreateApp (D5) already returns 403 on this combination,
        so this value should be unreachable on a properly-validated
        app.
      - `operator-disabled` — operator opt-in env is off; visible
        only on the gatewayd side, not this probe.
      - `upgrade-bypass` — request is an HTTP/1.1 Upgrade (e.g.
        WebSocket) and bypasses the streaming path.

    `effective_cap_bytes` is the plan cap (`cap_kind="plan"`) when
    no route shape is supplied. A route-aware probe may return
    `cap_kind="endpoint-rule"` when the compiled gatewayd rule
    matches; gatewayd failures and non-matches fall back to the
    plan cap. A customer who needs the canonical live status still
    fires a real request and reads the `Streaming-Status` response
    header.

    """

    app_id: str
    status: AppStreamingStatusStatus
    effective_cap_bytes: int
    plan_cap_bytes: int
    flag_enabled: bool
    plan_allowed: bool
    cap_kind: AppStreamingStatusCapKind | Unset = UNSET
    """`plan` is the plan-level fallback, `endpoint-rule` is a
    matching compiled gatewayd response cap, and `none` is
    reserved for a future explicit no-cap result.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        status: str = self.status

        effective_cap_bytes = self.effective_cap_bytes

        plan_cap_bytes = self.plan_cap_bytes

        flag_enabled = self.flag_enabled

        plan_allowed = self.plan_allowed

        cap_kind: str | Unset = UNSET
        if not isinstance(self.cap_kind, Unset):
            cap_kind = self.cap_kind

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "status": status,
                "effective_cap_bytes": effective_cap_bytes,
                "plan_cap_bytes": plan_cap_bytes,
                "flag_enabled": flag_enabled,
                "plan_allowed": plan_allowed,
            }
        )
        if cap_kind is not UNSET:
            field_dict["cap_kind"] = cap_kind

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        status = check_app_streaming_status_status(d.pop("status"))

        effective_cap_bytes = d.pop("effective_cap_bytes")

        plan_cap_bytes = d.pop("plan_cap_bytes")

        flag_enabled = d.pop("flag_enabled")

        plan_allowed = d.pop("plan_allowed")

        _cap_kind = d.pop("cap_kind", UNSET)
        cap_kind: AppStreamingStatusCapKind | Unset
        if isinstance(_cap_kind, Unset):
            cap_kind = UNSET
        else:
            cap_kind = check_app_streaming_status_cap_kind(_cap_kind)

        app_streaming_status = cls(
            app_id=app_id,
            status=status,
            effective_cap_bytes=effective_cap_bytes,
            plan_cap_bytes=plan_cap_bytes,
            flag_enabled=flag_enabled,
            plan_allowed=plan_allowed,
            cap_kind=cap_kind,
        )

        app_streaming_status.additional_properties = d
        return app_streaming_status

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
