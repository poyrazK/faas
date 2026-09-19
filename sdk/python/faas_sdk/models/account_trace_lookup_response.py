from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.account_trace_invocation import AccountTraceInvocation
    from ..models.account_trace_lookup_error import AccountTraceLookupError
    from ..models.account_trace_match import AccountTraceMatch
    from ..models.debug_telemetry_span import DebugTelemetrySpan


T = TypeVar("T", bound="AccountTraceLookupResponse")


@_attrs_define
class AccountTraceLookupResponse:
    """Tenant-scoped distributed-trace correlation envelope."""

    trace_id: str
    generated_at: datetime.datetime
    limit: int
    matches: list[AccountTraceMatch]
    invocations: list[AccountTraceInvocation]
    spans: list[DebugTelemetrySpan]
    spans_truncated: bool
    partial: bool | Unset = UNSET
    errors: list[AccountTraceLookupError] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        trace_id = self.trace_id

        generated_at = self.generated_at.isoformat()

        limit = self.limit

        matches = []
        for matches_item_data in self.matches:
            matches_item = matches_item_data.to_dict()
            matches.append(matches_item)

        invocations = []
        for invocations_item_data in self.invocations:
            invocations_item = invocations_item_data.to_dict()
            invocations.append(invocations_item)

        spans = []
        for spans_item_data in self.spans:
            spans_item = spans_item_data.to_dict()
            spans.append(spans_item)

        spans_truncated = self.spans_truncated

        partial = self.partial

        errors: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.errors, Unset):
            errors = []
            for errors_item_data in self.errors:
                errors_item = errors_item_data.to_dict()
                errors.append(errors_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "trace_id": trace_id,
                "generated_at": generated_at,
                "limit": limit,
                "matches": matches,
                "invocations": invocations,
                "spans": spans,
                "spans_truncated": spans_truncated,
            }
        )
        if partial is not UNSET:
            field_dict["partial"] = partial
        if errors is not UNSET:
            field_dict["errors"] = errors

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.account_trace_invocation import AccountTraceInvocation
        from ..models.account_trace_lookup_error import AccountTraceLookupError
        from ..models.account_trace_match import AccountTraceMatch
        from ..models.debug_telemetry_span import DebugTelemetrySpan

        d = dict(src_dict)
        trace_id = d.pop("trace_id")

        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        limit = d.pop("limit")

        matches = []
        _matches = d.pop("matches")
        for matches_item_data in _matches:
            matches_item = AccountTraceMatch.from_dict(matches_item_data)

            matches.append(matches_item)

        invocations = []
        _invocations = d.pop("invocations")
        for invocations_item_data in _invocations:
            invocations_item = AccountTraceInvocation.from_dict(invocations_item_data)

            invocations.append(invocations_item)

        spans = []
        _spans = d.pop("spans")
        for spans_item_data in _spans:
            spans_item = DebugTelemetrySpan.from_dict(spans_item_data)

            spans.append(spans_item)

        spans_truncated = d.pop("spans_truncated")

        partial = d.pop("partial", UNSET)

        _errors = d.pop("errors", UNSET)
        errors: list[AccountTraceLookupError] | Unset = UNSET
        if _errors is not UNSET:
            errors = []
            for errors_item_data in _errors:
                errors_item = AccountTraceLookupError.from_dict(errors_item_data)

                errors.append(errors_item)

        account_trace_lookup_response = cls(
            trace_id=trace_id,
            generated_at=generated_at,
            limit=limit,
            matches=matches,
            invocations=invocations,
            spans=spans,
            spans_truncated=spans_truncated,
            partial=partial,
            errors=errors,
        )

        account_trace_lookup_response.additional_properties = d
        return account_trace_lookup_response

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
