from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.fire_cron_response_status import FireCronResponseStatus, check_fire_cron_response_status

T = TypeVar("T", bound="FireCronResponse")


@_attrs_define
class FireCronResponse:
    """Issue #791 PR-C / ADR-090. The 202 body for `POST /v1/crons/{id}/run`.
    `request_id` is the durable handle on the fire-now row in
    `cron_fire_now_requests` (migrations/00193); poll
    `GET /v1/crons/{id}/runs` for the matching `cron.fired.manually`
    audit row, or watch the future `GET /v1/cron-fire-now-requests/{id}`
    for terminal status. `status` is `"pending"` at the moment of
    the response; transitions to `"succeeded"` or `"failed"`
    asynchronously as schedd claims the row.

    """

    request_id: str
    cron_id: str
    status: FireCronResponseStatus
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = self.request_id

        cron_id = self.cron_id

        status: str = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_id": request_id,
                "cron_id": cron_id,
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = d.pop("request_id")

        cron_id = d.pop("cron_id")

        status = check_fire_cron_response_status(d.pop("status"))

        fire_cron_response = cls(
            request_id=request_id,
            cron_id=cron_id,
            status=status,
        )

        fire_cron_response.additional_properties = d
        return fire_cron_response

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
