from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_policy_plan_authority import RoutePolicyPlanAuthority, check_route_policy_plan_authority
from ..models.route_policy_plan_status import RoutePolicyPlanStatus, check_route_policy_plan_status
from ..models.route_policy_plan_version import RoutePolicyPlanVersion, check_route_policy_plan_version
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_plan_unresolved import RoutePlanUnresolved
    from ..models.route_policy_change import RoutePolicyChange
    from ..models.route_policy_rule_usage import RoutePolicyRuleUsage
    from ..models.route_requirements_config import RouteRequirementsConfig
    from ..models.route_requirements_report import RouteRequirementsReport


T = TypeVar("T", bound="RoutePolicyPlan")


@_attrs_define
class RoutePolicyPlan:
    """Server proposal binding normalized requirements, current configuration, options, and proposed changes. Version 2
    uses concrete requirements; version 3 also binds the captured deployment contract and full inventory impact. Saved
    plans additionally bind requirements_revision and requirements_sha256; apply rejects changed saved intent.

    """

    version: RoutePolicyPlanVersion
    app: str
    status: RoutePolicyPlanStatus
    configuration_sha256: str
    requirements_sha256: str
    scope: str
    before: RouteRequirementsReport
    """Configuration evidence for concrete requests or every captured operation with group assignments and bounded
    policy scope."""
    after: RouteRequirementsReport
    """Configuration evidence for concrete requests or every captured operation with group assignments and bounded
    policy scope."""
    changes: list[RoutePolicyChange]
    unresolved: list[RoutePlanUnresolved]
    authority: RoutePolicyPlanAuthority | Unset = UNSET
    requirements: RouteRequirementsConfig | Unset = UNSET
    """Version 1 requires 1..500 concrete routes. Version 2 assigns every captured operation to groups, concrete
    routes, or public exceptions; overlapping groups are conjunctive."""
    throttle_burst: int | Unset = UNSET
    consolidate_budgets: bool | Unset = UNSET
    """Opt-in version 3 budget synthesis within declared group prefixes, including uncaptured and future paths."""
    rule_usage: RoutePolicyRuleUsage | Unset = UNSET
    """App rule count before and after the proposed changes and current plan quota, including disabled rules. No
    existing rules are deleted."""
    deployment_id: UUID | Unset = UNSET
    app_id: str | Unset = UNSET
    host: str | Unset = UNSET
    plan_name: str | Unset = UNSET
    sha256: str | Unset = UNSET
    requirements_revision: int | Unset = UNSET
    """Present when planning from saved requirements; part of the reviewed fingerprint."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        app = self.app

        status: str = self.status

        configuration_sha256 = self.configuration_sha256

        requirements_sha256 = self.requirements_sha256

        scope = self.scope

        before = self.before.to_dict()

        after = self.after.to_dict()

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        unresolved = []
        for unresolved_item_data in self.unresolved:
            unresolved_item = unresolved_item_data.to_dict()
            unresolved.append(unresolved_item)

        authority: str | Unset = UNSET
        if not isinstance(self.authority, Unset):
            authority = self.authority

        requirements: dict[str, Any] | Unset = UNSET
        if not isinstance(self.requirements, Unset):
            requirements = self.requirements.to_dict()

        throttle_burst = self.throttle_burst

        consolidate_budgets = self.consolidate_budgets

        rule_usage: dict[str, Any] | Unset = UNSET
        if not isinstance(self.rule_usage, Unset):
            rule_usage = self.rule_usage.to_dict()

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        app_id = self.app_id

        host = self.host

        plan_name = self.plan_name

        sha256 = self.sha256

        requirements_revision = self.requirements_revision

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "app": app,
                "status": status,
                "configuration_sha256": configuration_sha256,
                "requirements_sha256": requirements_sha256,
                "scope": scope,
                "before": before,
                "after": after,
                "changes": changes,
                "unresolved": unresolved,
            }
        )
        if authority is not UNSET:
            field_dict["authority"] = authority
        if requirements is not UNSET:
            field_dict["requirements"] = requirements
        if throttle_burst is not UNSET:
            field_dict["throttle_burst"] = throttle_burst
        if consolidate_budgets is not UNSET:
            field_dict["consolidate_budgets"] = consolidate_budgets
        if rule_usage is not UNSET:
            field_dict["rule_usage"] = rule_usage
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if host is not UNSET:
            field_dict["host"] = host
        if plan_name is not UNSET:
            field_dict["plan_name"] = plan_name
        if sha256 is not UNSET:
            field_dict["sha256"] = sha256
        if requirements_revision is not UNSET:
            field_dict["requirements_revision"] = requirements_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_plan_unresolved import RoutePlanUnresolved
        from ..models.route_policy_change import RoutePolicyChange
        from ..models.route_policy_rule_usage import RoutePolicyRuleUsage
        from ..models.route_requirements_config import RouteRequirementsConfig
        from ..models.route_requirements_report import RouteRequirementsReport

        d = dict(src_dict)
        version = check_route_policy_plan_version(d.pop("version"))

        app = d.pop("app")

        status = check_route_policy_plan_status(d.pop("status"))

        configuration_sha256 = d.pop("configuration_sha256")

        requirements_sha256 = d.pop("requirements_sha256")

        scope = d.pop("scope")

        before = RouteRequirementsReport.from_dict(d.pop("before"))

        after = RouteRequirementsReport.from_dict(d.pop("after"))

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = RoutePolicyChange.from_dict(changes_item_data)

            changes.append(changes_item)

        unresolved = []
        _unresolved = d.pop("unresolved")
        for unresolved_item_data in _unresolved:
            unresolved_item = RoutePlanUnresolved.from_dict(unresolved_item_data)

            unresolved.append(unresolved_item)

        _authority = d.pop("authority", UNSET)
        authority: RoutePolicyPlanAuthority | Unset
        if isinstance(_authority, Unset):
            authority = UNSET
        else:
            authority = check_route_policy_plan_authority(_authority)

        _requirements = d.pop("requirements", UNSET)
        requirements: RouteRequirementsConfig | Unset
        if isinstance(_requirements, Unset):
            requirements = UNSET
        else:
            requirements = RouteRequirementsConfig.from_dict(_requirements)

        throttle_burst = d.pop("throttle_burst", UNSET)

        consolidate_budgets = d.pop("consolidate_budgets", UNSET)

        _rule_usage = d.pop("rule_usage", UNSET)
        rule_usage: RoutePolicyRuleUsage | Unset
        if isinstance(_rule_usage, Unset):
            rule_usage = UNSET
        else:
            rule_usage = RoutePolicyRuleUsage.from_dict(_rule_usage)

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        app_id = d.pop("app_id", UNSET)

        host = d.pop("host", UNSET)

        plan_name = d.pop("plan_name", UNSET)

        sha256 = d.pop("sha256", UNSET)

        requirements_revision = d.pop("requirements_revision", UNSET)

        route_policy_plan = cls(
            version=version,
            app=app,
            status=status,
            configuration_sha256=configuration_sha256,
            requirements_sha256=requirements_sha256,
            scope=scope,
            before=before,
            after=after,
            changes=changes,
            unresolved=unresolved,
            authority=authority,
            requirements=requirements,
            throttle_burst=throttle_burst,
            consolidate_budgets=consolidate_budgets,
            rule_usage=rule_usage,
            deployment_id=deployment_id,
            app_id=app_id,
            host=host,
            plan_name=plan_name,
            sha256=sha256,
            requirements_revision=requirements_revision,
        )

        route_policy_plan.additional_properties = d
        return route_policy_plan

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
