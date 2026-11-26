from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_sandbox_approval_check_stale_reason import (
    APISandboxApprovalCheckStaleReason,
    check_api_sandbox_approval_check_stale_reason,
)
from ..models.api_sandbox_approval_check_verdict import (
    APISandboxApprovalCheckVerdict,
    check_api_sandbox_approval_check_verdict,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_sandbox_comparison_report import APISandboxComparisonReport


T = TypeVar("T", bound="APISandboxApprovalCheck")


@_attrs_define
class APISandboxApprovalCheck:
    """Comparison evidence attached to a held release, with its current verdict."""

    comparison: APISandboxComparisonReport
    """Paired scenario results and the verdict for a sandbox comparison."""
    verdict: APISandboxApprovalCheckVerdict
    """Current verdict for release review."""
    stale_reason: APISandboxApprovalCheckStaleReason | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        comparison = self.comparison.to_dict()

        verdict: str = self.verdict

        stale_reason: str | Unset = UNSET
        if not isinstance(self.stale_reason, Unset):
            stale_reason = self.stale_reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "comparison": comparison,
                "verdict": verdict,
            }
        )
        if stale_reason is not UNSET:
            field_dict["stale_reason"] = stale_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_comparison_report import APISandboxComparisonReport

        d = dict(src_dict)
        comparison = APISandboxComparisonReport.from_dict(d.pop("comparison"))

        verdict = check_api_sandbox_approval_check_verdict(d.pop("verdict"))

        _stale_reason = d.pop("stale_reason", UNSET)
        stale_reason: APISandboxApprovalCheckStaleReason | Unset
        if isinstance(_stale_reason, Unset):
            stale_reason = UNSET
        else:
            stale_reason = check_api_sandbox_approval_check_stale_reason(_stale_reason)

        api_sandbox_approval_check = cls(
            comparison=comparison,
            verdict=verdict,
            stale_reason=stale_reason,
        )

        api_sandbox_approval_check.additional_properties = d
        return api_sandbox_approval_check

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
