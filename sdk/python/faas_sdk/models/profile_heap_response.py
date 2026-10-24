from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_heap_response_kind import ProfileHeapResponseKind, check_profile_heap_response_kind
from ..models.profile_heap_response_unit import ProfileHeapResponseUnit, check_profile_heap_response_unit
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_capture import ProfileCapture
    from ..models.profile_capture_function import ProfileCaptureFunction
    from ..models.profile_capture_stack import ProfileCaptureStack
    from ..models.profile_query import ProfileQuery
    from ..models.profile_source import ProfileSource


T = TypeVar("T", bound="ProfileHeapResponse")


@_attrs_define
class ProfileHeapResponse:
    """Continuous live heap near query.end (ADR-1155)."""

    query: ProfileQuery
    """Authorized deployment CPU capture window."""
    kind: ProfileHeapResponseKind
    unit: ProfileHeapResponseUnit
    total: int
    functions: list[ProfileCaptureFunction]
    flamegraph: ProfileCaptureStack
    """One frame in a bounded call-path tree."""
    empty: bool
    capture: ProfileCapture | Unset = UNSET
    """An on-demand capture. processes counts instrumented processes that saw the capture; zero means the instance
    has no collector."""
    source: ProfileSource | Unset = UNSET
    """Deployment-recorded GitHub provenance. Links use a full immutable commit SHA; uploaded or generated source
    is not verified against that commit. Unavailable provenance is explicit."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        query = self.query.to_dict()

        kind: str = self.kind

        unit: str = self.unit

        total = self.total

        functions = []
        for functions_item_data in self.functions:
            functions_item = functions_item_data.to_dict()
            functions.append(functions_item)

        flamegraph = self.flamegraph.to_dict()

        empty = self.empty

        capture: dict[str, Any] | Unset = UNSET
        if not isinstance(self.capture, Unset):
            capture = self.capture.to_dict()

        source: dict[str, Any] | Unset = UNSET
        if not isinstance(self.source, Unset):
            source = self.source.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "query": query,
                "kind": kind,
                "unit": unit,
                "total": total,
                "functions": functions,
                "flamegraph": flamegraph,
                "empty": empty,
            }
        )
        if capture is not UNSET:
            field_dict["capture"] = capture
        if source is not UNSET:
            field_dict["source"] = source

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_capture import ProfileCapture
        from ..models.profile_capture_function import ProfileCaptureFunction
        from ..models.profile_capture_stack import ProfileCaptureStack
        from ..models.profile_query import ProfileQuery
        from ..models.profile_source import ProfileSource

        d = dict(src_dict)
        query = ProfileQuery.from_dict(d.pop("query"))

        kind = check_profile_heap_response_kind(d.pop("kind"))

        unit = check_profile_heap_response_unit(d.pop("unit"))

        total = d.pop("total")

        functions = []
        _functions = d.pop("functions")
        for functions_item_data in _functions:
            functions_item = ProfileCaptureFunction.from_dict(functions_item_data)

            functions.append(functions_item)

        flamegraph = ProfileCaptureStack.from_dict(d.pop("flamegraph"))

        empty = d.pop("empty")

        _capture = d.pop("capture", UNSET)
        capture: ProfileCapture | Unset
        if isinstance(_capture, Unset):
            capture = UNSET
        else:
            capture = ProfileCapture.from_dict(_capture)

        _source = d.pop("source", UNSET)
        source: ProfileSource | Unset
        if isinstance(_source, Unset):
            source = UNSET
        else:
            source = ProfileSource.from_dict(_source)

        profile_heap_response = cls(
            query=query,
            kind=kind,
            unit=unit,
            total=total,
            functions=functions,
            flamegraph=flamegraph,
            empty=empty,
            capture=capture,
            source=source,
        )

        profile_heap_response.additional_properties = d
        return profile_heap_response

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
