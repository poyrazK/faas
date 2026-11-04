from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.selected_trace_lookup_response_invocations_status import (
    SelectedTraceLookupResponseInvocationsStatus,
    check_selected_trace_lookup_response_invocations_status,
)
from ..models.selected_trace_lookup_response_requests_status import (
    SelectedTraceLookupResponseRequestsStatus,
    check_selected_trace_lookup_response_requests_status,
)
from ..models.selected_trace_lookup_response_selection import (
    SelectedTraceLookupResponseSelection,
    check_selected_trace_lookup_response_selection,
)

if TYPE_CHECKING:
    from ..models.selected_trace_app import SelectedTraceApp
    from ..models.selected_trace_invocation import SelectedTraceInvocation
    from ..models.selected_trace_request import SelectedTraceRequest


T = TypeVar("T", bound="SelectedTraceLookupResponse")


@_attrs_define
class SelectedTraceLookupResponse:
    """Independent selected-app source reads, not an atomic snapshot or a complete trace. Empty retained results do not
    establish health. Both unavailable sources still return an explicit partial response.

    """

    selection: SelectedTraceLookupResponseSelection
    trace_id: str
    apps: list[SelectedTraceApp]
    generated_at: datetime.datetime
    limit: int
    request_window_start: datetime.datetime
    request_window_end: datetime.datetime
    request_retention_days: int
    requests: list[SelectedTraceRequest]
    requests_status: SelectedTraceLookupResponseRequestsStatus
    requests_truncated: bool
    invocations: list[SelectedTraceInvocation]
    invocations_status: SelectedTraceLookupResponseInvocationsStatus
    invocations_truncated: bool
    partial: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        selection: str = self.selection

        trace_id = self.trace_id

        apps = []
        for apps_item_data in self.apps:
            apps_item = apps_item_data.to_dict()
            apps.append(apps_item)

        generated_at = self.generated_at.isoformat()

        limit = self.limit

        request_window_start = self.request_window_start.isoformat()

        request_window_end = self.request_window_end.isoformat()

        request_retention_days = self.request_retention_days

        requests = []
        for requests_item_data in self.requests:
            requests_item = requests_item_data.to_dict()
            requests.append(requests_item)

        requests_status: str = self.requests_status

        requests_truncated = self.requests_truncated

        invocations = []
        for invocations_item_data in self.invocations:
            invocations_item = invocations_item_data.to_dict()
            invocations.append(invocations_item)

        invocations_status: str = self.invocations_status

        invocations_truncated = self.invocations_truncated

        partial = self.partial

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "selection": selection,
                "trace_id": trace_id,
                "apps": apps,
                "generated_at": generated_at,
                "limit": limit,
                "request_window_start": request_window_start,
                "request_window_end": request_window_end,
                "request_retention_days": request_retention_days,
                "requests": requests,
                "requests_status": requests_status,
                "requests_truncated": requests_truncated,
                "invocations": invocations,
                "invocations_status": invocations_status,
                "invocations_truncated": invocations_truncated,
                "partial": partial,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.selected_trace_app import SelectedTraceApp
        from ..models.selected_trace_invocation import SelectedTraceInvocation
        from ..models.selected_trace_request import SelectedTraceRequest

        d = dict(src_dict)
        selection = check_selected_trace_lookup_response_selection(d.pop("selection"))

        trace_id = d.pop("trace_id")

        apps = []
        _apps = d.pop("apps")
        for apps_item_data in _apps:
            apps_item = SelectedTraceApp.from_dict(apps_item_data)

            apps.append(apps_item)

        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        limit = d.pop("limit")

        request_window_start = datetime.datetime.fromisoformat(d.pop("request_window_start"))

        request_window_end = datetime.datetime.fromisoformat(d.pop("request_window_end"))

        request_retention_days = d.pop("request_retention_days")

        requests = []
        _requests = d.pop("requests")
        for requests_item_data in _requests:
            requests_item = SelectedTraceRequest.from_dict(requests_item_data)

            requests.append(requests_item)

        requests_status = check_selected_trace_lookup_response_requests_status(d.pop("requests_status"))

        requests_truncated = d.pop("requests_truncated")

        invocations = []
        _invocations = d.pop("invocations")
        for invocations_item_data in _invocations:
            invocations_item = SelectedTraceInvocation.from_dict(invocations_item_data)

            invocations.append(invocations_item)

        invocations_status = check_selected_trace_lookup_response_invocations_status(d.pop("invocations_status"))

        invocations_truncated = d.pop("invocations_truncated")

        partial = d.pop("partial")

        selected_trace_lookup_response = cls(
            selection=selection,
            trace_id=trace_id,
            apps=apps,
            generated_at=generated_at,
            limit=limit,
            request_window_start=request_window_start,
            request_window_end=request_window_end,
            request_retention_days=request_retention_days,
            requests=requests,
            requests_status=requests_status,
            requests_truncated=requests_truncated,
            invocations=invocations,
            invocations_status=invocations_status,
            invocations_truncated=invocations_truncated,
            partial=partial,
        )

        selected_trace_lookup_response.additional_properties = d
        return selected_trace_lookup_response

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
