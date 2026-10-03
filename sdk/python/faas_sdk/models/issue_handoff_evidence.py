from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="IssueHandoffEvidence")


@_attrs_define
class IssueHandoffEvidence:
    """Relative API paths requiring the receiver's own authenticated read access; reporting tokens and webhook signatures
    grant no access.

    """

    issue_path: str
    request_path: str | Unset = UNSET
    trace_path: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        issue_path = self.issue_path

        request_path = self.request_path

        trace_path = self.trace_path

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "issue_path": issue_path,
            }
        )
        if request_path is not UNSET:
            field_dict["request_path"] = request_path
        if trace_path is not UNSET:
            field_dict["trace_path"] = trace_path

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        issue_path = d.pop("issue_path")

        request_path = d.pop("request_path", UNSET)

        trace_path = d.pop("trace_path", UNSET)

        issue_handoff_evidence = cls(
            issue_path=issue_path,
            request_path=request_path,
            trace_path=trace_path,
        )

        issue_handoff_evidence.additional_properties = d
        return issue_handoff_evidence

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
