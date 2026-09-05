from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_deployment_request_tag_type_1 import (
    CreateDeploymentRequestTagType1,
    check_create_deployment_request_tag_type_1,
)
from ..models.create_deployment_request_tag_type_2_type_1 import (
    CreateDeploymentRequestTagType2Type1,
    check_create_deployment_request_tag_type_2_type_1,
)
from ..models.create_deployment_request_tag_type_3_type_1 import (
    CreateDeploymentRequestTagType3Type1,
    check_create_deployment_request_tag_type_3_type_1,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.canary_preset_spec import CanaryPresetSpec
    from ..models.create_deployment_overrides import CreateDeploymentOverrides
    from ..models.sidecar import Sidecar
    from ..models.workflow_spec import WorkflowSpec


T = TypeVar("T", bound="CreateDeploymentRequest")


@_attrs_define
class CreateDeploymentRequest:
    """Two content-types accepted (see operation description): prebuilt OCI image reference, or multipart source upload.
    The optional `overrides` object (issue #460 / ADR-053) lets a customer redeploy the same digest-pinned image with a
    different entrypoint / cmd / env / env_secrets / port / healthcheck without rebuilding the image. The override field
    list is FROZEN — six fields, no more — and any extra field on the override object 400s the request (the handler's
    decoder rejects unknown keys; see ADR-053 §Decision 1). The optional `sidecars` array (issue #463 / ADR-068)
    attaches up to 2 stateless sidecars (1 init + 1 sidecar) per app — a one-shot DB migrator as `init`, a metrics
    scraper as `sidecar`. nil/omitted = no sidecars.

    """

    image: str | Unset = UNSET
    """registry.gregale.dev/...@sha256:... — digest-pinned OCI reference."""
    overrides: CreateDeploymentOverrides | None | Unset = UNSET
    """Deploy-time overrides (entrypoint, cmd, env, env_secrets, port, healthcheck). nil/omitted = deploy the image
    as-is."""
    require_signed: bool | None | Unset = UNSET
    """Per-deploy signature-enforcement opt-in (issue #472 / ADR-054). nil = inherit apps.require_signed; *true is
    a no-op when the app flag is already on; *false is rejected with 403 deploy_signature_invalid when the app flag
    is on (operator policy wins)."""
    sidecars: list[Sidecar] | Unset = UNSET
    """Up to 2 stateless sidecars (1 init + 1 sidecar). nil/omitted = no sidecars. See ADR-068 for the hard 2-cap
    and stateless-only contract."""
    workflows: list[WorkflowSpec] | Unset = UNSET
    """Workflow DAG definitions for this deployment. Paid-plan only; persisted with the deployment and snapshotted
    at run start."""
    traffic_percent: int | None | Unset = UNSET
    """Per-deployment traffic-split weight (issue #556 PR-A). nil = server default 100; explicit 0..100 = opt into
    canary (Pro/Scale only)."""
    scope: None | str | Unset = UNSET
    """Top-level per-deployment env scope (ADR-091 / PR-D). Lowercase alnum + dash, 3..40 chars, no
    leading/trailing dash. nil/omitted = `default`."""
    reason: None | str | Unset = UNSET
    """Free-form operator note (issue #977 / ADR-116). DB CHECK enforces length(reason) <= 280."""
    tag: (
        CreateDeploymentRequestTagType1
        | CreateDeploymentRequestTagType2Type1
        | CreateDeploymentRequestTagType3Type1
        | None
        | Unset
    ) = UNSET
    """Closed-set annotation tag. DB CHECK (deployments_tag_set_chk) enforces the same vocabulary."""
    deployed_by: None | str | Unset = UNSET
    """Operator label. CLI auto-captures from `git config user.name`; githubd stamps pusher.name; Action defaults
    to ${{ github.actor }}."""
    pr_number: int | None | Unset = UNSET
    """PR number (when known). 0 / NULL collapses to NULL on the row (DB CHECK rejects 0)."""
    rollback_on_5xx: bool | None | Unset = UNSET
    """Per-deployment auto-rollback opt-in (issue #961 leaf 8 / ADR-118 / Mega-C PR-2). Pro+ only. nil = server
    default false."""
    canary: CanaryPresetSpec | None | Unset = UNSET
    """Per-deployment canary ladder (issue #976 / ADR-122 / SAFE-RELEASES-A). nil/omitted = server default 'none'.
    For preset='custom', stages carries the customer ladder."""
    full_rootfs_allow_auto: bool | None | Unset = UNSET
    """Whether to auto-fallback to a self-contained rootfs for images without a Gregale runtime base. Omitted uses
    the plan default."""
    full_rootfs_override: bool | None | Unset = UNSET
    """Tri-state full-rootfs override: null uses the plan default, true forces full-rootfs, false forces the
    shared-base path."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.canary_preset_spec import CanaryPresetSpec
        from ..models.create_deployment_overrides import CreateDeploymentOverrides

        image = self.image

        overrides: dict[str, Any] | None | Unset
        if isinstance(self.overrides, Unset):
            overrides = UNSET
        elif isinstance(self.overrides, CreateDeploymentOverrides):
            overrides = self.overrides.to_dict()
        else:
            overrides = self.overrides

        require_signed: bool | None | Unset
        if isinstance(self.require_signed, Unset):
            require_signed = UNSET
        else:
            require_signed = self.require_signed

        sidecars: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.sidecars, Unset):
            sidecars = []
            for sidecars_item_data in self.sidecars:
                sidecars_item = sidecars_item_data.to_dict()
                sidecars.append(sidecars_item)

        workflows: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.workflows, Unset):
            workflows = []
            for workflows_item_data in self.workflows:
                workflows_item = workflows_item_data.to_dict()
                workflows.append(workflows_item)

        traffic_percent: int | None | Unset
        if isinstance(self.traffic_percent, Unset):
            traffic_percent = UNSET
        else:
            traffic_percent = self.traffic_percent

        scope: None | str | Unset
        if isinstance(self.scope, Unset):
            scope = UNSET
        else:
            scope = self.scope

        reason: None | str | Unset
        if isinstance(self.reason, Unset):
            reason = UNSET
        else:
            reason = self.reason

        tag: None | str | Unset
        if isinstance(self.tag, Unset):
            tag = UNSET
        elif isinstance(self.tag, str):
            tag = self.tag
        elif isinstance(self.tag, str):
            tag = self.tag
        elif isinstance(self.tag, str):
            tag = self.tag
        else:
            tag = self.tag

        deployed_by: None | str | Unset
        if isinstance(self.deployed_by, Unset):
            deployed_by = UNSET
        else:
            deployed_by = self.deployed_by

        pr_number: int | None | Unset
        if isinstance(self.pr_number, Unset):
            pr_number = UNSET
        else:
            pr_number = self.pr_number

        rollback_on_5xx: bool | None | Unset
        if isinstance(self.rollback_on_5xx, Unset):
            rollback_on_5xx = UNSET
        else:
            rollback_on_5xx = self.rollback_on_5xx

        canary: dict[str, Any] | None | Unset
        if isinstance(self.canary, Unset):
            canary = UNSET
        elif isinstance(self.canary, CanaryPresetSpec):
            canary = self.canary.to_dict()
        else:
            canary = self.canary

        full_rootfs_allow_auto: bool | None | Unset
        if isinstance(self.full_rootfs_allow_auto, Unset):
            full_rootfs_allow_auto = UNSET
        else:
            full_rootfs_allow_auto = self.full_rootfs_allow_auto

        full_rootfs_override: bool | None | Unset
        if isinstance(self.full_rootfs_override, Unset):
            full_rootfs_override = UNSET
        else:
            full_rootfs_override = self.full_rootfs_override

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if image is not UNSET:
            field_dict["image"] = image
        if overrides is not UNSET:
            field_dict["overrides"] = overrides
        if require_signed is not UNSET:
            field_dict["require_signed"] = require_signed
        if sidecars is not UNSET:
            field_dict["sidecars"] = sidecars
        if workflows is not UNSET:
            field_dict["workflows"] = workflows
        if traffic_percent is not UNSET:
            field_dict["traffic_percent"] = traffic_percent
        if scope is not UNSET:
            field_dict["scope"] = scope
        if reason is not UNSET:
            field_dict["reason"] = reason
        if tag is not UNSET:
            field_dict["tag"] = tag
        if deployed_by is not UNSET:
            field_dict["deployed_by"] = deployed_by
        if pr_number is not UNSET:
            field_dict["pr_number"] = pr_number
        if rollback_on_5xx is not UNSET:
            field_dict["rollback_on_5xx"] = rollback_on_5xx
        if canary is not UNSET:
            field_dict["canary"] = canary
        if full_rootfs_allow_auto is not UNSET:
            field_dict["full_rootfs_allow_auto"] = full_rootfs_allow_auto
        if full_rootfs_override is not UNSET:
            field_dict["full_rootfs_override"] = full_rootfs_override

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.canary_preset_spec import CanaryPresetSpec
        from ..models.create_deployment_overrides import CreateDeploymentOverrides
        from ..models.sidecar import Sidecar
        from ..models.workflow_spec import WorkflowSpec

        d = dict(src_dict)
        image = d.pop("image", UNSET)

        def _parse_overrides(data: object) -> CreateDeploymentOverrides | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                overrides_type_0 = CreateDeploymentOverrides.from_dict(data)

                return overrides_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(CreateDeploymentOverrides | None | Unset, data)

        overrides = _parse_overrides(d.pop("overrides", UNSET))

        def _parse_require_signed(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        require_signed = _parse_require_signed(d.pop("require_signed", UNSET))

        _sidecars = d.pop("sidecars", UNSET)
        sidecars: list[Sidecar] | Unset = UNSET
        if _sidecars is not UNSET:
            sidecars = []
            for sidecars_item_data in _sidecars:
                sidecars_item = Sidecar.from_dict(sidecars_item_data)

                sidecars.append(sidecars_item)

        _workflows = d.pop("workflows", UNSET)
        workflows: list[WorkflowSpec] | Unset = UNSET
        if _workflows is not UNSET:
            workflows = []
            for workflows_item_data in _workflows:
                workflows_item = WorkflowSpec.from_dict(workflows_item_data)

                workflows.append(workflows_item)

        def _parse_traffic_percent(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        traffic_percent = _parse_traffic_percent(d.pop("traffic_percent", UNSET))

        def _parse_scope(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        scope = _parse_scope(d.pop("scope", UNSET))

        def _parse_reason(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        reason = _parse_reason(d.pop("reason", UNSET))

        def _parse_tag(
            data: object,
        ) -> (
            CreateDeploymentRequestTagType1
            | CreateDeploymentRequestTagType2Type1
            | CreateDeploymentRequestTagType3Type1
            | None
            | Unset
        ):
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                tag_type_1 = check_create_deployment_request_tag_type_1(data)

                return tag_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                tag_type_2_type_1 = check_create_deployment_request_tag_type_2_type_1(data)

                return tag_type_2_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                tag_type_3_type_1 = check_create_deployment_request_tag_type_3_type_1(data)

                return tag_type_3_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(
                CreateDeploymentRequestTagType1
                | CreateDeploymentRequestTagType2Type1
                | CreateDeploymentRequestTagType3Type1
                | None
                | Unset,
                data,
            )

        tag = _parse_tag(d.pop("tag", UNSET))

        def _parse_deployed_by(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        deployed_by = _parse_deployed_by(d.pop("deployed_by", UNSET))

        def _parse_pr_number(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        pr_number = _parse_pr_number(d.pop("pr_number", UNSET))

        def _parse_rollback_on_5xx(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        rollback_on_5xx = _parse_rollback_on_5xx(d.pop("rollback_on_5xx", UNSET))

        def _parse_canary(data: object) -> CanaryPresetSpec | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                canary_type_0 = CanaryPresetSpec.from_dict(data)

                return canary_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(CanaryPresetSpec | None | Unset, data)

        canary = _parse_canary(d.pop("canary", UNSET))

        def _parse_full_rootfs_allow_auto(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        full_rootfs_allow_auto = _parse_full_rootfs_allow_auto(d.pop("full_rootfs_allow_auto", UNSET))

        def _parse_full_rootfs_override(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        full_rootfs_override = _parse_full_rootfs_override(d.pop("full_rootfs_override", UNSET))

        create_deployment_request = cls(
            image=image,
            overrides=overrides,
            require_signed=require_signed,
            sidecars=sidecars,
            workflows=workflows,
            traffic_percent=traffic_percent,
            scope=scope,
            reason=reason,
            tag=tag,
            deployed_by=deployed_by,
            pr_number=pr_number,
            rollback_on_5xx=rollback_on_5xx,
            canary=canary,
            full_rootfs_allow_auto=full_rootfs_allow_auto,
            full_rootfs_override=full_rootfs_override,
        )

        create_deployment_request.additional_properties = d
        return create_deployment_request

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
