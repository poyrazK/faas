from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_tenant_statement_response_status import (
    PlatformTenantStatementResponseStatus,
    check_platform_tenant_statement_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.platform_tenant_statement_line_response import PlatformTenantStatementLineResponse


T = TypeVar("T", bound="PlatformTenantStatementResponse")


@_attrs_define
class PlatformTenantStatementResponse:
    """One immutable initial or additive adjustment revision for a cross-app customer period."""

    id: UUID
    tenant_id: UUID
    period_start: datetime.datetime
    period_end: datetime.datetime
    revision: int
    status: PlatformTenantStatementResponseStatus
    billable_units: int
    unpriced_units: int
    amount_millicents: int
    lines: list[PlatformTenantStatementLineResponse]
    as_of: datetime.datetime
    created_at: datetime.datetime
    currency: str | Unset = UNSET
    finalized_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        tenant_id = str(self.tenant_id)

        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        revision = self.revision

        status: str = self.status

        billable_units = self.billable_units

        unpriced_units = self.unpriced_units

        amount_millicents = self.amount_millicents

        lines = []
        for lines_item_data in self.lines:
            lines_item = lines_item_data.to_dict()
            lines.append(lines_item)

        as_of = self.as_of.isoformat()

        created_at = self.created_at.isoformat()

        currency = self.currency

        finalized_at: str | Unset = UNSET
        if not isinstance(self.finalized_at, Unset):
            finalized_at = self.finalized_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "tenant_id": tenant_id,
                "period_start": period_start,
                "period_end": period_end,
                "revision": revision,
                "status": status,
                "billable_units": billable_units,
                "unpriced_units": unpriced_units,
                "amount_millicents": amount_millicents,
                "lines": lines,
                "as_of": as_of,
                "created_at": created_at,
            }
        )
        if currency is not UNSET:
            field_dict["currency"] = currency
        if finalized_at is not UNSET:
            field_dict["finalized_at"] = finalized_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.platform_tenant_statement_line_response import PlatformTenantStatementLineResponse

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        tenant_id = UUID(d.pop("tenant_id"))

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

        revision = d.pop("revision")

        status = check_platform_tenant_statement_response_status(d.pop("status"))

        billable_units = d.pop("billable_units")

        unpriced_units = d.pop("unpriced_units")

        amount_millicents = d.pop("amount_millicents")

        lines = []
        _lines = d.pop("lines")
        for lines_item_data in _lines:
            lines_item = PlatformTenantStatementLineResponse.from_dict(lines_item_data)

            lines.append(lines_item)

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        currency = d.pop("currency", UNSET)

        _finalized_at = d.pop("finalized_at", UNSET)
        finalized_at: datetime.datetime | Unset
        if isinstance(_finalized_at, Unset):
            finalized_at = UNSET
        else:
            finalized_at = datetime.datetime.fromisoformat(_finalized_at)

        platform_tenant_statement_response = cls(
            id=id,
            tenant_id=tenant_id,
            period_start=period_start,
            period_end=period_end,
            revision=revision,
            status=status,
            billable_units=billable_units,
            unpriced_units=unpriced_units,
            amount_millicents=amount_millicents,
            lines=lines,
            as_of=as_of,
            created_at=created_at,
            currency=currency,
            finalized_at=finalized_at,
        )

        platform_tenant_statement_response.additional_properties = d
        return platform_tenant_statement_response

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
