from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.financial_budget_alert import FinancialBudgetAlert
    from ..models.financial_budget_evaluation import FinancialBudgetEvaluation
    from ..models.financial_budget_stop import FinancialBudgetStop
    from ..models.financial_compute_balance import FinancialComputeBalance


T = TypeVar("T", bound="FinancialBudgetRuntime")


@_attrs_define
class FinancialBudgetRuntime:
    """Evaluation and alerts cover the current revision and UTC month. Active manual stop decisions can carry over from
    earlier months until a policy mutation releases that revision.

    """

    alerts: list[FinancialBudgetAlert]
    compute: FinancialComputeBalance | Unset = UNSET
    """Conservative admission ledger. App/job budgets cap gross compute. Account budgets share included quantities;
    with meters compute and egress the monetary totals sum both meters under one paid limit and egress shows its
    contribution. Byte credits include framing and unused credits commit after physical retirement; these upper
    bounds can exceed invoice charges. Expired funding stays reserved until physical retirement. Subscriptions,
    storage, builds, object-storage delivery and taxes are excluded. Independent job attempts share account funding
    with apps."""
    evaluation: FinancialBudgetEvaluation | Unset = UNSET
    """Retained evidence boundary; an absent known_millicents means evaluation unavailable, never zero. Partial
    known costs can trigger labelled notifications; stale observations cannot create new alerts."""
    projection: FinancialBudgetAlert | Unset = UNSET
    """One threshold event per policy revision and UTC month. Edits re-evaluate monthly spending. Delivery is at
    least once with a stable transport idempotency key; delivered means accepted by the configured transport, not
    confirmed inbox receipt."""
    stops: list[FinancialBudgetStop] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        alerts = []
        for alerts_item_data in self.alerts:
            alerts_item = alerts_item_data.to_dict()
            alerts.append(alerts_item)

        compute: dict[str, Any] | Unset = UNSET
        if not isinstance(self.compute, Unset):
            compute = self.compute.to_dict()

        evaluation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.evaluation, Unset):
            evaluation = self.evaluation.to_dict()

        projection: dict[str, Any] | Unset = UNSET
        if not isinstance(self.projection, Unset):
            projection = self.projection.to_dict()

        stops: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.stops, Unset):
            stops = []
            for stops_item_data in self.stops:
                stops_item = stops_item_data.to_dict()
                stops.append(stops_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "alerts": alerts,
            }
        )
        if compute is not UNSET:
            field_dict["compute"] = compute
        if evaluation is not UNSET:
            field_dict["evaluation"] = evaluation
        if projection is not UNSET:
            field_dict["projection"] = projection
        if stops is not UNSET:
            field_dict["stops"] = stops

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_budget_alert import FinancialBudgetAlert
        from ..models.financial_budget_evaluation import FinancialBudgetEvaluation
        from ..models.financial_budget_stop import FinancialBudgetStop
        from ..models.financial_compute_balance import FinancialComputeBalance

        d = dict(src_dict)
        alerts = []
        _alerts = d.pop("alerts")
        for alerts_item_data in _alerts:
            alerts_item = FinancialBudgetAlert.from_dict(alerts_item_data)

            alerts.append(alerts_item)

        _compute = d.pop("compute", UNSET)
        compute: FinancialComputeBalance | Unset
        if isinstance(_compute, Unset):
            compute = UNSET
        else:
            compute = FinancialComputeBalance.from_dict(_compute)

        _evaluation = d.pop("evaluation", UNSET)
        evaluation: FinancialBudgetEvaluation | Unset
        if isinstance(_evaluation, Unset):
            evaluation = UNSET
        else:
            evaluation = FinancialBudgetEvaluation.from_dict(_evaluation)

        _projection = d.pop("projection", UNSET)
        projection: FinancialBudgetAlert | Unset
        if isinstance(_projection, Unset):
            projection = UNSET
        else:
            projection = FinancialBudgetAlert.from_dict(_projection)

        _stops = d.pop("stops", UNSET)
        stops: list[FinancialBudgetStop] | Unset = UNSET
        if _stops is not UNSET:
            stops = []
            for stops_item_data in _stops:
                stops_item = FinancialBudgetStop.from_dict(stops_item_data)

                stops.append(stops_item)

        financial_budget_runtime = cls(
            alerts=alerts,
            compute=compute,
            evaluation=evaluation,
            projection=projection,
            stops=stops,
        )

        financial_budget_runtime.additional_properties = d
        return financial_budget_runtime

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
