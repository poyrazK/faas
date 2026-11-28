from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.account_trace_wake import AccountTraceWake
    from ..models.debug_telemetry_request_item import DebugTelemetryRequestItem


T = TypeVar("T", bound="AccountTraceMatch")


@_attrs_define
class AccountTraceMatch:
    """One retained request-telemetry match for the trace."""

    app: str
    request: DebugTelemetryRequestItem
    """A retained request-ID mapping, optionally enriched with a bounded telemetry row when detailed evidence is
    available."""
    wake: AccountTraceWake | Unset = UNSET
    """How a matched request's app was woken, when its wake history is still retained. Omitted for warm requests."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        request = self.request.to_dict()

        wake: dict[str, Any] | Unset = UNSET
        if not isinstance(self.wake, Unset):
            wake = self.wake.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "request": request,
            }
        )
        if wake is not UNSET:
            field_dict["wake"] = wake

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.account_trace_wake import AccountTraceWake
        from ..models.debug_telemetry_request_item import DebugTelemetryRequestItem

        d = dict(src_dict)
        app = d.pop("app")

        request = DebugTelemetryRequestItem.from_dict(d.pop("request"))

        _wake = d.pop("wake", UNSET)
        wake: AccountTraceWake | Unset
        if isinstance(_wake, Unset):
            wake = UNSET
        else:
            wake = AccountTraceWake.from_dict(_wake)

        account_trace_match = cls(
            app=app,
            request=request,
            wake=wake,
        )

        account_trace_match.additional_properties = d
        return account_trace_match

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
