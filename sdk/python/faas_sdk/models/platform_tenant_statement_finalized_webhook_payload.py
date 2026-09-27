from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.platform_tenant_statement_finalized_webhook_payload_status import (
    PlatformTenantStatementFinalizedWebhookPayloadStatus,
    check_platform_tenant_statement_finalized_webhook_payload_status,
)

if TYPE_CHECKING:
    from ..models.platform_tenant_statement_line_response import PlatformTenantStatementLineResponse


T = TypeVar("T", bound="PlatformTenantStatementFinalizedWebhookPayload")


@_attrs_define
class PlatformTenantStatementFinalizedWebhookPayload:
    """Immutable cross-app statement snapshot delivered once per finalized revision to tenant-scoped receivers."""

    platform_tenant_id: UUID
    external_ref: str
    """Platform owner's stable customer reference."""
    statement_id: UUID
    revision: int
    status: PlatformTenantStatementFinalizedWebhookPayloadStatus
    period_start: datetime.datetime
    period_end: datetime.datetime
    currency: str
    billable_units: int
    unpriced_units: int
    amount_millicents: int
    priced: bool
    lines: list[PlatformTenantStatementLineResponse]
    as_of: datetime.datetime
    finalized_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        platform_tenant_id = str(self.platform_tenant_id)

        external_ref = self.external_ref

        statement_id = str(self.statement_id)

        revision = self.revision

        status: str = self.status

        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        currency = self.currency

        billable_units = self.billable_units

        unpriced_units = self.unpriced_units

        amount_millicents = self.amount_millicents

        priced = self.priced

        lines = []
        for lines_item_data in self.lines:
            lines_item = lines_item_data.to_dict()
            lines.append(lines_item)

        as_of = self.as_of.isoformat()

        finalized_at = self.finalized_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "platform_tenant_id": platform_tenant_id,
                "external_ref": external_ref,
                "statement_id": statement_id,
                "revision": revision,
                "status": status,
                "period_start": period_start,
                "period_end": period_end,
                "currency": currency,
                "billable_units": billable_units,
                "unpriced_units": unpriced_units,
                "amount_millicents": amount_millicents,
                "priced": priced,
                "lines": lines,
                "as_of": as_of,
                "finalized_at": finalized_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.platform_tenant_statement_line_response import PlatformTenantStatementLineResponse

        d = dict(src_dict)
        platform_tenant_id = UUID(d.pop("platform_tenant_id"))

        external_ref = d.pop("external_ref")

        statement_id = UUID(d.pop("statement_id"))

        revision = d.pop("revision")

        status = check_platform_tenant_statement_finalized_webhook_payload_status(d.pop("status"))

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

        currency = d.pop("currency")

        billable_units = d.pop("billable_units")

        unpriced_units = d.pop("unpriced_units")

        amount_millicents = d.pop("amount_millicents")

        priced = d.pop("priced")

        lines = []
        _lines = d.pop("lines")
        for lines_item_data in _lines:
            lines_item = PlatformTenantStatementLineResponse.from_dict(lines_item_data)

            lines.append(lines_item)

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        finalized_at = datetime.datetime.fromisoformat(d.pop("finalized_at"))

        platform_tenant_statement_finalized_webhook_payload = cls(
            platform_tenant_id=platform_tenant_id,
            external_ref=external_ref,
            statement_id=statement_id,
            revision=revision,
            status=status,
            period_start=period_start,
            period_end=period_end,
            currency=currency,
            billable_units=billable_units,
            unpriced_units=unpriced_units,
            amount_millicents=amount_millicents,
            priced=priced,
            lines=lines,
            as_of=as_of,
            finalized_at=finalized_at,
        )

        return platform_tenant_statement_finalized_webhook_payload
