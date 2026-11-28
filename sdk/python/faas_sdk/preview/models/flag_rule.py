from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.flag_rule_rollout_unit import FlagRuleRolloutUnit, check_flag_rule_rollout_unit
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.flag_condition import FlagCondition
    from ..models.flag_rule_experiment import FlagRuleExperiment
    from ..models.progressive_rollout import ProgressiveRollout


T = TypeVar("T", bound="FlagRule")


@_attrs_define
class FlagRule:
    """Ordered targeting rule; customer/group and subject constraints combine with AND. Subject rules require a customer or
    group constraint. Subject IDs are opaque application IDs and are evaluated only after application authentication.
    Boolean flags require a boolean value; variant flags may omit value to use weighted assignment. Progressive rollout
    is limited to boolean true rules.

    """

    id: str
    customers: list[UUID] | Unset = UNSET
    group: str | Unset = UNSET
    """Owner-managed customer group key."""
    subjects: list[str] | Unset = UNSET
    """Opaque application subject IDs, matched inside the selected customers/group. Use stable internal IDs, not
    emails or display names."""
    rollout: int | Unset = UNSET
    """Basis points of eligible customers by default, or eligible subjects when rollout_unit is subject; omitted
    means all eligible targets."""
    rollout_unit: FlagRuleRolloutUnit | Unset = UNSET
    """Allocation unit for rollout percentages. Omitted preserves customer-level allocation. Subject rollout
    requires a customer or group constraint and an authenticated subject context."""
    attributes: list[FlagCondition] | Unset = UNSET
    """Request attribute conditions; all must hold. Runtime SDKs need capability attribute-targeting-v1."""
    progression: ProgressiveRollout | Unset = UNSET
    """Health-gated stages for a boolean true rule. Promotion is manual by default; auto_advance opts into server-
    managed promotion after a full healthy evidence window. The rule rollout must equal stages[current_stage];
    stages must strictly increase and end at 10000 basis points."""
    value: bool | str | Unset = UNSET
    experiment: FlagRuleExperiment | Unset = UNSET
    """The running experiment of a weighted rule, set when the experiment starts and cleared when it stops. Apid
    writes it; a configuration update must carry it unchanged. Runtime SDKs need capabilities experiment-layers-v1
    and subject-exposures-v1."""

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        customers: list[str] | Unset = UNSET
        if not isinstance(self.customers, Unset):
            customers = []
            for customers_item_data in self.customers:
                customers_item = str(customers_item_data)
                customers.append(customers_item)

        group = self.group

        subjects: list[str] | Unset = UNSET
        if not isinstance(self.subjects, Unset):
            subjects = self.subjects

        rollout = self.rollout

        rollout_unit: str | Unset = UNSET
        if not isinstance(self.rollout_unit, Unset):
            rollout_unit = self.rollout_unit

        attributes: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.attributes, Unset):
            attributes = []
            for attributes_item_data in self.attributes:
                attributes_item = attributes_item_data.to_dict()
                attributes.append(attributes_item)

        progression: dict[str, Any] | Unset = UNSET
        if not isinstance(self.progression, Unset):
            progression = self.progression.to_dict()

        value: bool | str | Unset
        if isinstance(self.value, Unset):
            value = UNSET
        else:
            value = self.value

        experiment: dict[str, Any] | Unset = UNSET
        if not isinstance(self.experiment, Unset):
            experiment = self.experiment.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
            }
        )
        if customers is not UNSET:
            field_dict["customers"] = customers
        if group is not UNSET:
            field_dict["group"] = group
        if subjects is not UNSET:
            field_dict["subjects"] = subjects
        if rollout is not UNSET:
            field_dict["rollout"] = rollout
        if rollout_unit is not UNSET:
            field_dict["rollout_unit"] = rollout_unit
        if attributes is not UNSET:
            field_dict["attributes"] = attributes
        if progression is not UNSET:
            field_dict["progression"] = progression
        if value is not UNSET:
            field_dict["value"] = value
        if experiment is not UNSET:
            field_dict["experiment"] = experiment

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_condition import FlagCondition
        from ..models.flag_rule_experiment import FlagRuleExperiment
        from ..models.progressive_rollout import ProgressiveRollout

        d = dict(src_dict)
        id = d.pop("id")

        _customers = d.pop("customers", UNSET)
        customers: list[UUID] | Unset = UNSET
        if _customers is not UNSET:
            customers = []
            for customers_item_data in _customers:
                customers_item = UUID(customers_item_data)

                customers.append(customers_item)

        group = d.pop("group", UNSET)

        subjects = cast(list[str], d.pop("subjects", UNSET))

        rollout = d.pop("rollout", UNSET)

        _rollout_unit = d.pop("rollout_unit", UNSET)
        rollout_unit: FlagRuleRolloutUnit | Unset
        if isinstance(_rollout_unit, Unset):
            rollout_unit = UNSET
        else:
            rollout_unit = check_flag_rule_rollout_unit(_rollout_unit)

        _attributes = d.pop("attributes", UNSET)
        attributes: list[FlagCondition] | Unset = UNSET
        if _attributes is not UNSET:
            attributes = []
            for attributes_item_data in _attributes:
                attributes_item = FlagCondition.from_dict(attributes_item_data)

                attributes.append(attributes_item)

        _progression = d.pop("progression", UNSET)
        progression: ProgressiveRollout | Unset
        if isinstance(_progression, Unset):
            progression = UNSET
        else:
            progression = ProgressiveRollout.from_dict(_progression)

        def _parse_value(data: object) -> bool | str | Unset:
            if isinstance(data, Unset):
                return data
            return cast(bool | str | Unset, data)

        value = _parse_value(d.pop("value", UNSET))

        _experiment = d.pop("experiment", UNSET)
        experiment: FlagRuleExperiment | Unset
        if isinstance(_experiment, Unset):
            experiment = UNSET
        else:
            experiment = FlagRuleExperiment.from_dict(_experiment)

        flag_rule = cls(
            id=id,
            customers=customers,
            group=group,
            subjects=subjects,
            rollout=rollout,
            rollout_unit=rollout_unit,
            attributes=attributes,
            progression=progression,
            value=value,
            experiment=experiment,
        )

        return flag_rule
