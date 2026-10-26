from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.financial_budget_response_status import (
    FinancialBudgetResponseStatus,
    check_financial_budget_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.financial_budget_runtime import FinancialBudgetRuntime
    from ..models.financial_budget_spec import FinancialBudgetSpec


T = TypeVar("T", bound="FinancialBudgetResponse")


@_attrs_define
class FinancialBudgetResponse:
    """Saved policy intent with current-revision monitoring, alert delivery and preview stop acknowledgements.
    Notifications alone never establish workload stopping.

    """

    id: UUID
    account_id: UUID
    revision: int
    spec: FinancialBudgetSpec
    """Customer budget intent; activation and enforcement are separately acknowledged."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    status: FinancialBudgetResponseStatus
    enforcement_ready: bool
    """True when the selected stopping integration is available; actual per-target teardown is reported in
    runtime.stops. Strict compute balances are reported in runtime.compute."""
    reasons: list[str]
    deleted_at: datetime.datetime | Unset = UNSET
    notification_ready: bool | Unset = UNSET
    """True for an enabled supported monitored policy; inspect runtime for evaluation freshness and delivery
    status."""
    runtime: FinancialBudgetRuntime | Unset = UNSET
    """Evaluation and alerts cover the current revision and UTC month. Active manual stop decisions can carry over
    from earlier months until a policy mutation releases that revision."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        account_id = str(self.account_id)

        revision = self.revision

        spec = self.spec.to_dict()

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        status: str = self.status

        enforcement_ready = self.enforcement_ready

        reasons = self.reasons

        deleted_at: str | Unset = UNSET
        if not isinstance(self.deleted_at, Unset):
            deleted_at = self.deleted_at.isoformat()

        notification_ready = self.notification_ready

        runtime: dict[str, Any] | Unset = UNSET
        if not isinstance(self.runtime, Unset):
            runtime = self.runtime.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "account_id": account_id,
                "revision": revision,
                "spec": spec,
                "created_at": created_at,
                "updated_at": updated_at,
                "status": status,
                "enforcement_ready": enforcement_ready,
                "reasons": reasons,
            }
        )
        if deleted_at is not UNSET:
            field_dict["deleted_at"] = deleted_at
        if notification_ready is not UNSET:
            field_dict["notification_ready"] = notification_ready
        if runtime is not UNSET:
            field_dict["runtime"] = runtime

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_budget_runtime import FinancialBudgetRuntime
        from ..models.financial_budget_spec import FinancialBudgetSpec

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        account_id = UUID(d.pop("account_id"))

        revision = d.pop("revision")

        spec = FinancialBudgetSpec.from_dict(d.pop("spec"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        status = check_financial_budget_response_status(d.pop("status"))

        enforcement_ready = d.pop("enforcement_ready")

        reasons = cast(list[str], d.pop("reasons"))

        _deleted_at = d.pop("deleted_at", UNSET)
        deleted_at: datetime.datetime | Unset
        if isinstance(_deleted_at, Unset):
            deleted_at = UNSET
        else:
            deleted_at = datetime.datetime.fromisoformat(_deleted_at)

        notification_ready = d.pop("notification_ready", UNSET)

        _runtime = d.pop("runtime", UNSET)
        runtime: FinancialBudgetRuntime | Unset
        if isinstance(_runtime, Unset):
            runtime = UNSET
        else:
            runtime = FinancialBudgetRuntime.from_dict(_runtime)

        financial_budget_response = cls(
            id=id,
            account_id=account_id,
            revision=revision,
            spec=spec,
            created_at=created_at,
            updated_at=updated_at,
            status=status,
            enforcement_ready=enforcement_ready,
            reasons=reasons,
            deleted_at=deleted_at,
            notification_ready=notification_ready,
            runtime=runtime,
        )

        financial_budget_response.additional_properties = d
        return financial_budget_response

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
