from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.issue_split_matcher_source_kind import IssueSplitMatcherSourceKind, check_issue_split_matcher_source_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="IssueSplitMatcher")


@_attrs_define
class IssueSplitMatcher:
    """Exact equality against sanitized stored evidence; every populated field must match. At least one nonempty selector
    is required. Empty fields are wildcards.

    """

    exception_type: str | Unset = UNSET
    error_code: str | Unset = UNSET
    route: str | Unset = UNSET
    source_kind: IssueSplitMatcherSourceKind | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        exception_type = self.exception_type

        error_code = self.error_code

        route = self.route

        source_kind: str | Unset = UNSET
        if not isinstance(self.source_kind, Unset):
            source_kind = self.source_kind

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if exception_type is not UNSET:
            field_dict["exception_type"] = exception_type
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if route is not UNSET:
            field_dict["route"] = route
        if source_kind is not UNSET:
            field_dict["source_kind"] = source_kind

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        exception_type = d.pop("exception_type", UNSET)

        error_code = d.pop("error_code", UNSET)

        route = d.pop("route", UNSET)

        _source_kind = d.pop("source_kind", UNSET)
        source_kind: IssueSplitMatcherSourceKind | Unset
        if isinstance(_source_kind, Unset):
            source_kind = UNSET
        else:
            source_kind = check_issue_split_matcher_source_kind(_source_kind)

        issue_split_matcher = cls(
            exception_type=exception_type,
            error_code=error_code,
            route=route,
            source_kind=source_kind,
        )

        return issue_split_matcher
