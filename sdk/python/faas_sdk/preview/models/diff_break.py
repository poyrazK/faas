from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.diff_break_severity import DiffBreakSeverity, check_diff_break_severity
from ..types import UNSET, Unset

T = TypeVar("T", bound="DiffBreak")


@_attrs_define
class DiffBreak:
    """One would-ship-a-problem row. Stable RFC 7807 code
    (matches [api.Code*] constants) so the CLI error renders
    identically to what a real deploy would say.

    """

    code: str
    """Stable RFC 7807 code; matches api.Code* constants"""
    severity: DiffBreakSeverity
    reason: str
    field: str | Unset = UNSET
    """Optional scope-wide ('memory') or per-row ('environment.<scope>.<key>')."""
    observed: Any | Unset = UNSET
    """JSON-encoded observed value (int / string / []string / …)."""
    limit: Any | Unset = UNSET
    """JSON-encoded limit value."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        severity: str = self.severity

        reason = self.reason

        field = self.field

        observed = self.observed

        limit = self.limit

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "code": code,
                "severity": severity,
                "reason": reason,
            }
        )
        if field is not UNSET:
            field_dict["field"] = field
        if observed is not UNSET:
            field_dict["observed"] = observed
        if limit is not UNSET:
            field_dict["limit"] = limit

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        severity = check_diff_break_severity(d.pop("severity"))

        reason = d.pop("reason")

        field = d.pop("field", UNSET)

        observed = d.pop("observed", UNSET)

        limit = d.pop("limit", UNSET)

        diff_break = cls(
            code=code,
            severity=severity,
            reason=reason,
            field=field,
            observed=observed,
            limit=limit,
        )

        diff_break.additional_properties = d
        return diff_break

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
