from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.gdpr_audit_export_response_action import (
    GdprAuditExportResponseAction,
    check_gdpr_audit_export_response_action,
)
from ..models.gdpr_audit_export_response_source import (
    GdprAuditExportResponseSource,
    check_gdpr_audit_export_response_source,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.gdpr_audit_export_response_data import GdprAuditExportResponseData


T = TypeVar("T", bound="GdprAuditExportResponse")


@_attrs_define
class GdprAuditExportResponse:
    """One row of the customer's audit trail in the GDPR export bundle. Two row kinds live here: source=`gdpr` carries a
    self-service action (`export`, `delete`, `restore`); source=`event` carries a security event from the events table
    (IAM-4, ADR-035) with a `kind` and JSON `data` payload. The two are interleaved by timestamp descending.

    """

    source: GdprAuditExportResponseSource
    requested_at: datetime.datetime
    action: GdprAuditExportResponseAction | Unset = UNSET
    completed_at: datetime.datetime | None | Unset = UNSET
    kind: str | Unset = UNSET
    """ Security event kind. Populated only when `source` = `event`. Examples: `auth.login`, `key.created`,
    `secret.set`, `account.deletion_scheduled`. """
    data: GdprAuditExportResponseData | Unset = UNSET
    """ Kind-specific JSON payload from the events row. Populated only when `source` = `event`. Plaintext values
    (e.g. secret VALUE) are NEVER carried in `data`. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source: str = self.source

        requested_at = self.requested_at.isoformat()

        action: str | Unset = UNSET
        if not isinstance(self.action, Unset):
            action = self.action

        completed_at: None | str | Unset
        if isinstance(self.completed_at, Unset):
            completed_at = UNSET
        elif isinstance(self.completed_at, datetime.datetime):
            completed_at = self.completed_at.isoformat()
        else:
            completed_at = self.completed_at

        kind = self.kind

        data: dict[str, Any] | Unset = UNSET
        if not isinstance(self.data, Unset):
            data = self.data.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "requested_at": requested_at,
            }
        )
        if action is not UNSET:
            field_dict["action"] = action
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at
        if kind is not UNSET:
            field_dict["kind"] = kind
        if data is not UNSET:
            field_dict["data"] = data

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.gdpr_audit_export_response_data import GdprAuditExportResponseData

        d = dict(src_dict)
        source = check_gdpr_audit_export_response_source(d.pop("source"))

        requested_at = datetime.datetime.fromisoformat(d.pop("requested_at"))

        _action = d.pop("action", UNSET)
        action: GdprAuditExportResponseAction | Unset
        if isinstance(_action, Unset):
            action = UNSET
        else:
            action = check_gdpr_audit_export_response_action(_action)

        def _parse_completed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                completed_at_type_0 = datetime.datetime.fromisoformat(data)

                return completed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        completed_at = _parse_completed_at(d.pop("completed_at", UNSET))

        kind = d.pop("kind", UNSET)

        _data = d.pop("data", UNSET)
        data: GdprAuditExportResponseData | Unset
        if isinstance(_data, Unset):
            data = UNSET
        else:
            data = GdprAuditExportResponseData.from_dict(_data)

        gdpr_audit_export_response = cls(
            source=source,
            requested_at=requested_at,
            action=action,
            completed_at=completed_at,
            kind=kind,
            data=data,
        )

        gdpr_audit_export_response.additional_properties = d
        return gdpr_audit_export_response

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
