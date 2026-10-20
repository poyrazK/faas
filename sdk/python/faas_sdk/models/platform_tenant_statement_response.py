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
    from ..models.api_consumer_statement_fee_response import APIConsumerStatementFeeResponse
    from ..models.platform_tenant_statement_line_response import PlatformTenantStatementLineResponse


T = TypeVar("T", bound="PlatformTenantStatementResponse")


@_attrs_define
class PlatformTenantStatementResponse:
    """One immutable initial or additive adjustment revision for a cross-app customer period, with compact invoice lines
    and private minute-level adjustment coverage.

    """

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
    fees: list[APIConsumerStatementFeeResponse] | Unset = UNSET
    """Monthly base fees and minimum top-ups this revision bills (ADR-1322); amount_millicents includes them. A
    revision can bill fees without lines for a tenant without usage."""
    credit_applied_millicents: int | Unset = UNSET
    """Prepaid credit drawn when the revision was finalized (ADR-20261019100331)."""
    amount_due_millicents: int | Unset = UNSET
    """amount_millicents less credit_applied_millicents: what invoice exports bill."""
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

        fees: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.fees, Unset):
            fees = []
            for fees_item_data in self.fees:
                fees_item = fees_item_data.to_dict()
                fees.append(fees_item)

        credit_applied_millicents = self.credit_applied_millicents

        amount_due_millicents = self.amount_due_millicents

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
        if fees is not UNSET:
            field_dict["fees"] = fees
        if credit_applied_millicents is not UNSET:
            field_dict["credit_applied_millicents"] = credit_applied_millicents
        if amount_due_millicents is not UNSET:
            field_dict["amount_due_millicents"] = amount_due_millicents
        if finalized_at is not UNSET:
            field_dict["finalized_at"] = finalized_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_statement_fee_response import APIConsumerStatementFeeResponse
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

        _fees = d.pop("fees", UNSET)
        fees: list[APIConsumerStatementFeeResponse] | Unset = UNSET
        if _fees is not UNSET:
            fees = []
            for fees_item_data in _fees:
                fees_item = APIConsumerStatementFeeResponse.from_dict(fees_item_data)

                fees.append(fees_item)

        credit_applied_millicents = d.pop("credit_applied_millicents", UNSET)

        amount_due_millicents = d.pop("amount_due_millicents", UNSET)

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
            fees=fees,
            credit_applied_millicents=credit_applied_millicents,
            amount_due_millicents=amount_due_millicents,
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
