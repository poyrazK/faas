from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.financial_compute_balance_basis import FinancialComputeBalanceBasis, check_financial_compute_balance_basis
from ..models.financial_compute_balance_scope_kind import (
    FinancialComputeBalanceScopeKind,
    check_financial_compute_balance_scope_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.financial_egress_balance import FinancialEgressBalance


T = TypeVar("T", bound="FinancialComputeBalance")


@_attrs_define
class FinancialComputeBalance:
    """Conservative admission ledger. App/job budgets cap gross compute. Account budgets share included quantities; with
    meters compute and egress the monetary totals sum both meters under one paid limit and egress shows its
    contribution. Byte credits include framing and unused credits commit after physical retirement; these upper bounds
    can exceed invoice charges. Expired funding stays reserved until physical retirement. Subscriptions, storage,
    builds, object-storage delivery and taxes are excluded. Independent job attempts share account funding with apps.

    """

    initialized: bool
    period_start: datetime.datetime
    period_end: datetime.datetime
    committed_millicents: int
    reserved_millicents: int
    remaining_millicents: int
    unconfirmed_leases: int
    held: bool
    observed_at: datetime.datetime
    egress: FinancialEgressBalance | Unset = UNSET
    """Egress contribution to a shared account compute and egress budget. Its funding allowance is pinned to the
    lowest recorded grant for the UTC month; increases benefit funding in a new month. Funded interface bytes
    include framing margin. Unused or uncertain credits commit conservatively after physical absence; invoice usage
    remains measured bytes."""
    app_id: UUID | Unset = UNSET
    job_id: UUID | Unset = UNSET
    project_id: UUID | Unset = UNSET
    """The capped project of a strict project budget (ADR-1750, amendment 1): one gross book shared by every app in
    the project. Jobs are not covered."""
    account_id: UUID | Unset = UNSET
    scope_kind: FinancialComputeBalanceScopeKind | Unset = UNSET
    basis: FinancialComputeBalanceBasis | Unset = UNSET
    allowance_granted_mb_seconds: int | Unset = UNSET
    allowance_committed_mb_seconds: int | Unset = UNSET
    allowance_reserved_mb_seconds: int | Unset = UNSET
    allowance_remaining_mb_seconds: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        initialized = self.initialized

        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        committed_millicents = self.committed_millicents

        reserved_millicents = self.reserved_millicents

        remaining_millicents = self.remaining_millicents

        unconfirmed_leases = self.unconfirmed_leases

        held = self.held

        observed_at = self.observed_at.isoformat()

        egress: dict[str, Any] | Unset = UNSET
        if not isinstance(self.egress, Unset):
            egress = self.egress.to_dict()

        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        job_id: str | Unset = UNSET
        if not isinstance(self.job_id, Unset):
            job_id = str(self.job_id)

        project_id: str | Unset = UNSET
        if not isinstance(self.project_id, Unset):
            project_id = str(self.project_id)

        account_id: str | Unset = UNSET
        if not isinstance(self.account_id, Unset):
            account_id = str(self.account_id)

        scope_kind: str | Unset = UNSET
        if not isinstance(self.scope_kind, Unset):
            scope_kind = self.scope_kind

        basis: str | Unset = UNSET
        if not isinstance(self.basis, Unset):
            basis = self.basis

        allowance_granted_mb_seconds = self.allowance_granted_mb_seconds

        allowance_committed_mb_seconds = self.allowance_committed_mb_seconds

        allowance_reserved_mb_seconds = self.allowance_reserved_mb_seconds

        allowance_remaining_mb_seconds = self.allowance_remaining_mb_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "initialized": initialized,
                "period_start": period_start,
                "period_end": period_end,
                "committed_millicents": committed_millicents,
                "reserved_millicents": reserved_millicents,
                "remaining_millicents": remaining_millicents,
                "unconfirmed_leases": unconfirmed_leases,
                "held": held,
                "observed_at": observed_at,
            }
        )
        if egress is not UNSET:
            field_dict["egress"] = egress
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if account_id is not UNSET:
            field_dict["account_id"] = account_id
        if scope_kind is not UNSET:
            field_dict["scope_kind"] = scope_kind
        if basis is not UNSET:
            field_dict["basis"] = basis
        if allowance_granted_mb_seconds is not UNSET:
            field_dict["allowance_granted_mb_seconds"] = allowance_granted_mb_seconds
        if allowance_committed_mb_seconds is not UNSET:
            field_dict["allowance_committed_mb_seconds"] = allowance_committed_mb_seconds
        if allowance_reserved_mb_seconds is not UNSET:
            field_dict["allowance_reserved_mb_seconds"] = allowance_reserved_mb_seconds
        if allowance_remaining_mb_seconds is not UNSET:
            field_dict["allowance_remaining_mb_seconds"] = allowance_remaining_mb_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_egress_balance import FinancialEgressBalance

        d = dict(src_dict)
        initialized = d.pop("initialized")

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

        committed_millicents = d.pop("committed_millicents")

        reserved_millicents = d.pop("reserved_millicents")

        remaining_millicents = d.pop("remaining_millicents")

        unconfirmed_leases = d.pop("unconfirmed_leases")

        held = d.pop("held")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        _egress = d.pop("egress", UNSET)
        egress: FinancialEgressBalance | Unset
        if isinstance(_egress, Unset):
            egress = UNSET
        else:
            egress = FinancialEgressBalance.from_dict(_egress)

        _app_id = d.pop("app_id", UNSET)
        app_id: UUID | Unset
        if isinstance(_app_id, Unset):
            app_id = UNSET
        else:
            app_id = UUID(_app_id)

        _job_id = d.pop("job_id", UNSET)
        job_id: UUID | Unset
        if isinstance(_job_id, Unset):
            job_id = UNSET
        else:
            job_id = UUID(_job_id)

        _project_id = d.pop("project_id", UNSET)
        project_id: UUID | Unset
        if isinstance(_project_id, Unset):
            project_id = UNSET
        else:
            project_id = UUID(_project_id)

        _account_id = d.pop("account_id", UNSET)
        account_id: UUID | Unset
        if isinstance(_account_id, Unset):
            account_id = UNSET
        else:
            account_id = UUID(_account_id)

        _scope_kind = d.pop("scope_kind", UNSET)
        scope_kind: FinancialComputeBalanceScopeKind | Unset
        if isinstance(_scope_kind, Unset):
            scope_kind = UNSET
        else:
            scope_kind = check_financial_compute_balance_scope_kind(_scope_kind)

        _basis = d.pop("basis", UNSET)
        basis: FinancialComputeBalanceBasis | Unset
        if isinstance(_basis, Unset):
            basis = UNSET
        else:
            basis = check_financial_compute_balance_basis(_basis)

        allowance_granted_mb_seconds = d.pop("allowance_granted_mb_seconds", UNSET)

        allowance_committed_mb_seconds = d.pop("allowance_committed_mb_seconds", UNSET)

        allowance_reserved_mb_seconds = d.pop("allowance_reserved_mb_seconds", UNSET)

        allowance_remaining_mb_seconds = d.pop("allowance_remaining_mb_seconds", UNSET)

        financial_compute_balance = cls(
            initialized=initialized,
            period_start=period_start,
            period_end=period_end,
            committed_millicents=committed_millicents,
            reserved_millicents=reserved_millicents,
            remaining_millicents=remaining_millicents,
            unconfirmed_leases=unconfirmed_leases,
            held=held,
            observed_at=observed_at,
            egress=egress,
            app_id=app_id,
            job_id=job_id,
            project_id=project_id,
            account_id=account_id,
            scope_kind=scope_kind,
            basis=basis,
            allowance_granted_mb_seconds=allowance_granted_mb_seconds,
            allowance_committed_mb_seconds=allowance_committed_mb_seconds,
            allowance_reserved_mb_seconds=allowance_reserved_mb_seconds,
            allowance_remaining_mb_seconds=allowance_remaining_mb_seconds,
        )

        financial_compute_balance.additional_properties = d
        return financial_compute_balance

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
