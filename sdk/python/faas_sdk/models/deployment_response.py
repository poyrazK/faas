from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.deployment_response_deployed_via_type_1 import (
    DeploymentResponseDeployedViaType1,
    check_deployment_response_deployed_via_type_1,
)
from ..models.deployment_response_deployed_via_type_2_type_1 import (
    DeploymentResponseDeployedViaType2Type1,
    check_deployment_response_deployed_via_type_2_type_1,
)
from ..models.deployment_response_deployed_via_type_3_type_1 import (
    DeploymentResponseDeployedViaType3Type1,
    check_deployment_response_deployed_via_type_3_type_1,
)
from ..models.deployment_response_parked_reason_type_1 import (
    DeploymentResponseParkedReasonType1,
    check_deployment_response_parked_reason_type_1,
)
from ..models.deployment_response_parked_reason_type_2_type_1 import (
    DeploymentResponseParkedReasonType2Type1,
    check_deployment_response_parked_reason_type_2_type_1,
)
from ..models.deployment_response_parked_reason_type_3_type_1 import (
    DeploymentResponseParkedReasonType3Type1,
    check_deployment_response_parked_reason_type_3_type_1,
)
from ..models.deployment_response_tag import DeploymentResponseTag, check_deployment_response_tag
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.build_plan import BuildPlan
    from ..models.deployment_healthcheck import DeploymentHealthcheck
    from ..models.deployment_liveness_probe import DeploymentLivenessProbe
    from ..models.deployment_response_override_env_secret_refs import DeploymentResponseOverrideEnvSecretRefs
    from ..models.log_excerpt import LogExcerpt
    from ..models.scan_result import ScanResult
    from ..models.secret_scan_result import SecretScanResult


T = TypeVar("T", bound="DeploymentResponse")


@_attrs_define
class DeploymentResponse:
    """One deployment: id, app, source ref, build status, commit SHA, and lifecycle timestamps. The optional
    `has_overrides` and `override_*` fields are the persisted echo of the create-time overrides object (issue #460 /
    ADR-053); they round-trip via `GET /v1/apps/{slug}/deployments/{id}` so a customer can audit what their last deploy
    pinned. Env values are NEVER echoed — only the keys (`override_env_keys`); env_secrets refs ARE echoed because the
    ref shape is non-secret by design.

    """

    id: str
    app_id: str
    image_digest: str
    kind: str
    status: str
    created_at: datetime.datetime
    build_id: None | str | Unset = UNSET
    error: None | str | Unset = UNSET
    error_code: None | str | Unset = UNSET
    error_hint: None | str | Unset = UNSET
    """One-line next-action lifted from pkg/whycopy catalog."""
    error_why: None | str | Unset = UNSET
    """Human-readable cause with observed value."""
    error_fix: None | str | Unset = UNSET
    """Prescriptive remediation (1-3 lines)."""
    error_relevant_logs: list[LogExcerpt] | Unset = UNSET
    """Per-line log excerpts explaining the failure (error-explanations cluster). Capped at 20 entries × 512 bytes
    by the CLI tripwire."""
    has_overrides: bool | Unset = UNSET
    """True when this deployment carries a non-null override_* column set."""
    override_entrypoint: list[str] | Unset = UNSET
    """Entrypoint override echoed verbatim from the create request. nil when no override was supplied."""
    override_cmd: list[str] | Unset = UNSET
    """Cmd override echoed verbatim from the create request."""
    override_env_keys: list[str] | Unset = UNSET
    """Sorted set of env-var keys set by the env override. VALUES ARE NEVER ECHOED (ADR-053 §Decision 4)."""
    override_env_secret_keys: list[str] | Unset = UNSET
    """Sorted set of env-var keys set by the env_secrets override. The parallel refs are echoed in
    `override_env_secret_refs` because the ref shape is non-secret by design."""
    override_env_secret_refs: DeploymentResponseOverrideEnvSecretRefs | Unset = UNSET
    """Verbatim `secret:NAME` ref map; the customer needs to see which secret they bound to which env var to debug
    a misconfigured deploy."""
    override_port: int | Unset = UNSET
    """Listen-port override; 0 = absent (fall back to image default)."""
    override_healthcheck: DeploymentHealthcheck | None | Unset = UNSET
    """Readiness-probe override. Persisted verbatim; the actual HTTP probe is a follow-up — today waitReady stays a
    bare TCP accept."""
    override_liveness_probe: DeploymentLivenessProbe | None | Unset = UNSET
    """Liveness-probe override echoed verbatim (issue #554 / ADR-078). nil when the deployment used the per-plan
    default (Hobby/Pro/Scale → 5s / 3 consecutive / 60s cooldown). Echoed on GET /v1/apps/{slug}/deployments/{id} so
    the customer can audit which probe the host (cmd/vmmd) is running against the VM."""
    min_instances: int | Unset = UNSET
    """Per-deployment cold-wake floor override (issue #557 closure / ADR-072). 0 = inherit from parent app
    (default); positive value is the deployment's own floor. Effective per-instance floor =
    max(app.EffectiveMinInstances(), d.EffectiveMinInstances()). Validated against the parent app's plan
    MaxMinInstances cap on PATCH."""
    scan: None | ScanResult | Unset = UNSET
    """Per-deploy grype CVE scan surface (issue #464 / ADR-055). nil on pre-feature rows (the migration backfilled
    scan_status='skipped' + scan_result={reason: 'pre-feature'} on those; the apid read path returns nil so the
    dashboard / CLI see a clean absence — the /scan route surfaces the 'skipped' sentinel for those rows). Non-nil
    for post-feature rows in any of the {pending, complete, failed, skipped} states. The customer can deploy a
    CRITICAL-CVE image; the dashboard shows it; that is the contract (no enforcement at the deploy gate)."""
    parked_reason: (
        DeploymentResponseParkedReasonType1
        | DeploymentResponseParkedReasonType2Type1
        | DeploymentResponseParkedReasonType3Type1
        | None
        | Unset
    ) = UNSET
    """Per-deployment parking reason (issue #554 / ADR-079 follow-up, migration 00157). Closed-set vocabulary
    enforced at the schema layer via the deployments_parked_reason_check constraint. nil for never-parked
    deployments — surfaced as no field on the wire via omitempty."""
    parked_at: datetime.datetime | None | Unset = UNSET
    """Wall-clock timestamp the deployment was parked (set once, idempotent across schedd restart cycles). nil for
    never-parked deployments."""
    traffic_percent: int | Unset = UNSET
    """Per-deployment traffic-split weight (issue #556 PR-A). Summed across live rows for the app = 100 by
    construction."""
    scope: None | str | Unset = UNSET
    """Per-deployment env scope (ADR-091 / PR-D). Lowercase alnum + dash, 3..40 chars, no leading/trailing dash.
    nil/omitted = `default`."""
    secret_scan: None | SecretScanResult | Unset = UNSET
    """Per-deploy secret-scan audit row (PR-A / ADR-101). Mirrors
    the `scan` field shape — absent when the row has not been
    scanned yet (deploy mid-pipeline or pre-PR-A), present
    with `findings=[]` for a clean walk, present with
    one-or-more entries for a hit. Read by the dashboard's
    "secret scan" card and the CLI's `--show-secret-scan`
    flag. Stamped both for the imaged-side layer walk (main
    image + each sidecar; post-build, loud-fail on any
    finding) and — forward-compat — for the apid-side
    source-tree 422 path. Status closed-set the writer
    stamps: "complete" (clean) | "complete_with_redactions"
    (hit). The `image_digest` sub-field records which OCI
    digest the imaged walk ran against; null on legacy
    pre-PR-A rows. See `pkg/imaged/secretscan.go`.
    """
    build_plan: BuildPlan | None | Unset = UNSET
    """Auto-detected build plan (issue #961 / Mega-A PR-2). One-line summary the CLI prints after `gregale deploy`.
    nil for image deploys."""
    deployed_by_user_id: None | Unset | UUID = UNSET
    """UUID of the deploying local account (FK → accounts.id, ON DELETE SET NULL). Empty when the deploy came from
    a non-local source (e.g. a githubd pusher not bound to a local account)."""
    deployed_via: (
        DeploymentResponseDeployedViaType1
        | DeploymentResponseDeployedViaType2Type1
        | DeploymentResponseDeployedViaType3Type1
        | None
        | Unset
    ) = UNSET
    """Closed-set classifier of how this deployment was submitted. One of `api` (SDK / API key) / `cli` (bearer
    token) / `dashboard` (session cookie) / `github` (githubd_bridge) / `operator` (admin). Enforced at the schema
    layer by migrations/00303_deployments_actor.sql's CHECK constraint."""
    deployed_from_ip: None | str | Unset = UNSET
    """Trusted remote IP captured by `pkg/middleware.ClientIP` at handler entry (XFF + loopback trust contract).
    Loopback (127.0.0.1) for the githubd_bridge path. Both IPv4 and IPv6 are accepted at the wire and stored in
    Postgres' native `inet` type (which canonicalises both families); the OpenAPI schema intentionally omits
    `format: ipv4` so v6 deployments (which grow as the public gateway picks up AAAA records) do not fail schema
    validation. v6 is rendered as the bracketed colon-hex form per RFC 5952."""
    pusher_login: None | str | Unset = UNSET
    """Raw GitHub login of the pusher when `deployed_via == "github"`. Empty for all other via values. Distinct
    from the human-readable `DeployedBy` text column (issue #977 / PR #984) — pusher_login is the unmodified GH
    identity, suitable for downstream GitHub-API correlation."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.build_plan import BuildPlan
        from ..models.deployment_healthcheck import DeploymentHealthcheck
        from ..models.deployment_liveness_probe import DeploymentLivenessProbe
        from ..models.scan_result import ScanResult
        from ..models.secret_scan_result import SecretScanResult

        id = self.id

        app_id = self.app_id

        image_digest = self.image_digest

        kind = self.kind

        status = self.status

        created_at = self.created_at.isoformat()

        build_id: None | str | Unset
        if isinstance(self.build_id, Unset):
            build_id = UNSET
        else:
            build_id = self.build_id

        error: None | str | Unset
        if isinstance(self.error, Unset):
            error = UNSET
        else:
            error = self.error

        error_code: None | str | Unset
        if isinstance(self.error_code, Unset):
            error_code = UNSET
        else:
            error_code = self.error_code

        error_hint: None | str | Unset
        if isinstance(self.error_hint, Unset):
            error_hint = UNSET
        else:
            error_hint = self.error_hint

        error_why: None | str | Unset
        if isinstance(self.error_why, Unset):
            error_why = UNSET
        else:
            error_why = self.error_why

        error_fix: None | str | Unset
        if isinstance(self.error_fix, Unset):
            error_fix = UNSET
        else:
            error_fix = self.error_fix

        error_relevant_logs: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.error_relevant_logs, Unset):
            error_relevant_logs = []
            for error_relevant_logs_item_data in self.error_relevant_logs:
                error_relevant_logs_item = error_relevant_logs_item_data.to_dict()
                error_relevant_logs.append(error_relevant_logs_item)

        has_overrides = self.has_overrides

        override_entrypoint: list[str] | Unset = UNSET
        if not isinstance(self.override_entrypoint, Unset):
            override_entrypoint = self.override_entrypoint

        override_cmd: list[str] | Unset = UNSET
        if not isinstance(self.override_cmd, Unset):
            override_cmd = self.override_cmd

        override_env_keys: list[str] | Unset = UNSET
        if not isinstance(self.override_env_keys, Unset):
            override_env_keys = self.override_env_keys

        override_env_secret_keys: list[str] | Unset = UNSET
        if not isinstance(self.override_env_secret_keys, Unset):
            override_env_secret_keys = self.override_env_secret_keys

        override_env_secret_refs: dict[str, Any] | Unset = UNSET
        if not isinstance(self.override_env_secret_refs, Unset):
            override_env_secret_refs = self.override_env_secret_refs.to_dict()

        override_port = self.override_port

        override_healthcheck: dict[str, Any] | None | Unset
        if isinstance(self.override_healthcheck, Unset):
            override_healthcheck = UNSET
        elif isinstance(self.override_healthcheck, DeploymentHealthcheck):
            override_healthcheck = self.override_healthcheck.to_dict()
        else:
            override_healthcheck = self.override_healthcheck

        override_liveness_probe: dict[str, Any] | None | Unset
        if isinstance(self.override_liveness_probe, Unset):
            override_liveness_probe = UNSET
        elif isinstance(self.override_liveness_probe, DeploymentLivenessProbe):
            override_liveness_probe = self.override_liveness_probe.to_dict()
        else:
            override_liveness_probe = self.override_liveness_probe

        min_instances = self.min_instances

        scan: dict[str, Any] | None | Unset
        if isinstance(self.scan, Unset):
            scan = UNSET
        elif isinstance(self.scan, ScanResult):
            scan = self.scan.to_dict()
        else:
            scan = self.scan

        parked_reason: None | str | Unset
        if isinstance(self.parked_reason, Unset):
            parked_reason = UNSET
        elif isinstance(self.parked_reason, str):
            parked_reason = self.parked_reason
        elif isinstance(self.parked_reason, str):
            parked_reason = self.parked_reason
        elif isinstance(self.parked_reason, str):
            parked_reason = self.parked_reason
        else:
            parked_reason = self.parked_reason

        parked_at: None | str | Unset
        if isinstance(self.parked_at, Unset):
            parked_at = UNSET
        elif isinstance(self.parked_at, datetime.datetime):
            parked_at = self.parked_at.isoformat()
        else:
            parked_at = self.parked_at

        traffic_percent = self.traffic_percent

        scope: None | str | Unset
        if isinstance(self.scope, Unset):
            scope = UNSET
        else:
            scope = self.scope

        secret_scan: dict[str, Any] | None | Unset
        if isinstance(self.secret_scan, Unset):
            secret_scan = UNSET
        elif isinstance(self.secret_scan, SecretScanResult):
            secret_scan = self.secret_scan.to_dict()
        else:
            secret_scan = self.secret_scan

        build_plan: dict[str, Any] | None | Unset
        if isinstance(self.build_plan, Unset):
            build_plan = UNSET
        elif isinstance(self.build_plan, BuildPlan):
            build_plan = self.build_plan.to_dict()
        else:
            build_plan = self.build_plan

        deployed_by_user_id: None | str | Unset
        if isinstance(self.deployed_by_user_id, Unset):
            deployed_by_user_id = UNSET
        elif isinstance(self.deployed_by_user_id, UUID):
            deployed_by_user_id = str(self.deployed_by_user_id)
        else:
            deployed_by_user_id = self.deployed_by_user_id

        deployed_via: None | str | Unset
        if isinstance(self.deployed_via, Unset):
            deployed_via = UNSET
        elif isinstance(self.deployed_via, str):
            deployed_via = self.deployed_via
        elif isinstance(self.deployed_via, str):
            deployed_via = self.deployed_via
        elif isinstance(self.deployed_via, str):
            deployed_via = self.deployed_via
        else:
            deployed_via = self.deployed_via

        deployed_from_ip: None | str | Unset
        if isinstance(self.deployed_from_ip, Unset):
            deployed_from_ip = UNSET
        else:
            deployed_from_ip = self.deployed_from_ip

        pusher_login: None | str | Unset
        if isinstance(self.pusher_login, Unset):
            pusher_login = UNSET
        else:
            pusher_login = self.pusher_login

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "image_digest": image_digest,
                "kind": kind,
                "status": status,
                "created_at": created_at,
            }
        )
        if build_id is not UNSET:
            field_dict["build_id"] = build_id
        if error is not UNSET:
            field_dict["error"] = error
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if error_hint is not UNSET:
            field_dict["error_hint"] = error_hint
        if error_why is not UNSET:
            field_dict["error_why"] = error_why
        if error_fix is not UNSET:
            field_dict["error_fix"] = error_fix
        if error_relevant_logs is not UNSET:
            field_dict["error_relevant_logs"] = error_relevant_logs
        if has_overrides is not UNSET:
            field_dict["has_overrides"] = has_overrides
        if override_entrypoint is not UNSET:
            field_dict["override_entrypoint"] = override_entrypoint
        if override_cmd is not UNSET:
            field_dict["override_cmd"] = override_cmd
        if override_env_keys is not UNSET:
            field_dict["override_env_keys"] = override_env_keys
        if override_env_secret_keys is not UNSET:
            field_dict["override_env_secret_keys"] = override_env_secret_keys
        if override_env_secret_refs is not UNSET:
            field_dict["override_env_secret_refs"] = override_env_secret_refs
        if override_port is not UNSET:
            field_dict["override_port"] = override_port
        if override_healthcheck is not UNSET:
            field_dict["override_healthcheck"] = override_healthcheck
        if override_liveness_probe is not UNSET:
            field_dict["override_liveness_probe"] = override_liveness_probe
        if min_instances is not UNSET:
            field_dict["min_instances"] = min_instances
        if scan is not UNSET:
            field_dict["scan"] = scan
        if parked_reason is not UNSET:
            field_dict["parked_reason"] = parked_reason
        if parked_at is not UNSET:
            field_dict["parked_at"] = parked_at
        if traffic_percent is not UNSET:
            field_dict["traffic_percent"] = traffic_percent
        if scope is not UNSET:
            field_dict["scope"] = scope
        if secret_scan is not UNSET:
            field_dict["secret_scan"] = secret_scan
        if build_plan is not UNSET:
            field_dict["build_plan"] = build_plan
        if deployed_by_user_id is not UNSET:
            field_dict["deployed_by_user_id"] = deployed_by_user_id
        if deployed_via is not UNSET:
            field_dict["deployed_via"] = deployed_via
        if deployed_from_ip is not UNSET:
            field_dict["deployed_from_ip"] = deployed_from_ip
        if pusher_login is not UNSET:
            field_dict["pusher_login"] = pusher_login

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.build_plan import BuildPlan
        from ..models.deployment_healthcheck import DeploymentHealthcheck
        from ..models.deployment_liveness_probe import DeploymentLivenessProbe
        from ..models.deployment_response_override_env_secret_refs import DeploymentResponseOverrideEnvSecretRefs
        from ..models.log_excerpt import LogExcerpt
        from ..models.scan_result import ScanResult
        from ..models.secret_scan_result import SecretScanResult

        d = dict(src_dict)
        id = d.pop("id")

        app_id = d.pop("app_id")

        image_digest = d.pop("image_digest")

        kind = d.pop("kind")

        status = d.pop("status")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        def _parse_build_id(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        build_id = _parse_build_id(d.pop("build_id", UNSET))

        def _parse_error(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        error = _parse_error(d.pop("error", UNSET))

        def _parse_error_code(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        error_code = _parse_error_code(d.pop("error_code", UNSET))

        def _parse_error_hint(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        error_hint = _parse_error_hint(d.pop("error_hint", UNSET))

        def _parse_error_why(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        error_why = _parse_error_why(d.pop("error_why", UNSET))

        def _parse_error_fix(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        error_fix = _parse_error_fix(d.pop("error_fix", UNSET))

        _error_relevant_logs = d.pop("error_relevant_logs", UNSET)
        error_relevant_logs: list[LogExcerpt] | Unset = UNSET
        if _error_relevant_logs is not UNSET:
            error_relevant_logs = []
            for error_relevant_logs_item_data in _error_relevant_logs:
                error_relevant_logs_item = LogExcerpt.from_dict(error_relevant_logs_item_data)

                error_relevant_logs.append(error_relevant_logs_item)

        has_overrides = d.pop("has_overrides", UNSET)

        override_entrypoint = cast(list[str], d.pop("override_entrypoint", UNSET))

        override_cmd = cast(list[str], d.pop("override_cmd", UNSET))

        override_env_keys = cast(list[str], d.pop("override_env_keys", UNSET))

        override_env_secret_keys = cast(list[str], d.pop("override_env_secret_keys", UNSET))

        _override_env_secret_refs = d.pop("override_env_secret_refs", UNSET)
        override_env_secret_refs: DeploymentResponseOverrideEnvSecretRefs | Unset
        if isinstance(_override_env_secret_refs, Unset):
            override_env_secret_refs = UNSET
        else:
            override_env_secret_refs = DeploymentResponseOverrideEnvSecretRefs.from_dict(_override_env_secret_refs)

        override_port = d.pop("override_port", UNSET)

        def _parse_override_healthcheck(data: object) -> DeploymentHealthcheck | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                override_healthcheck_type_0 = DeploymentHealthcheck.from_dict(data)

                return override_healthcheck_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(DeploymentHealthcheck | None | Unset, data)

        override_healthcheck = _parse_override_healthcheck(d.pop("override_healthcheck", UNSET))

        def _parse_override_liveness_probe(data: object) -> DeploymentLivenessProbe | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                override_liveness_probe_type_0 = DeploymentLivenessProbe.from_dict(data)

                return override_liveness_probe_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(DeploymentLivenessProbe | None | Unset, data)

        override_liveness_probe = _parse_override_liveness_probe(d.pop("override_liveness_probe", UNSET))

        min_instances = d.pop("min_instances", UNSET)

        def _parse_scan(data: object) -> None | ScanResult | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                scan_type_0 = ScanResult.from_dict(data)

                return scan_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ScanResult | Unset, data)

        scan = _parse_scan(d.pop("scan", UNSET))

        def _parse_parked_reason(
            data: object,
        ) -> (
            DeploymentResponseParkedReasonType1
            | DeploymentResponseParkedReasonType2Type1
            | DeploymentResponseParkedReasonType3Type1
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
                parked_reason_type_1 = check_deployment_response_parked_reason_type_1(data)

                return parked_reason_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                parked_reason_type_2_type_1 = check_deployment_response_parked_reason_type_2_type_1(data)

                return parked_reason_type_2_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                parked_reason_type_3_type_1 = check_deployment_response_parked_reason_type_3_type_1(data)

                return parked_reason_type_3_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(
                DeploymentResponseParkedReasonType1
                | DeploymentResponseParkedReasonType2Type1
                | DeploymentResponseParkedReasonType3Type1
                | None
                | Unset,
                data,
            )

        parked_reason = _parse_parked_reason(d.pop("parked_reason", UNSET))

        def _parse_parked_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                parked_at_type_0 = datetime.datetime.fromisoformat(data)

                return parked_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        parked_at = _parse_parked_at(d.pop("parked_at", UNSET))

        traffic_percent = d.pop("traffic_percent", UNSET)

        def _parse_scope(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        scope = _parse_scope(d.pop("scope", UNSET))

        def _parse_secret_scan(data: object) -> None | SecretScanResult | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                secret_scan_type_0 = SecretScanResult.from_dict(data)

                return secret_scan_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | SecretScanResult | Unset, data)

        secret_scan = _parse_secret_scan(d.pop("secret_scan", UNSET))

        def _parse_build_plan(data: object) -> BuildPlan | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                build_plan_type_0 = BuildPlan.from_dict(data)

                return build_plan_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(BuildPlan | None | Unset, data)

        build_plan = _parse_build_plan(d.pop("build_plan", UNSET))

        def _parse_deployed_by_user_id(data: object) -> None | Unset | UUID:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                deployed_by_user_id_type_0 = UUID(data)

                return deployed_by_user_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | Unset | UUID, data)

        deployed_by_user_id = _parse_deployed_by_user_id(d.pop("deployed_by_user_id", UNSET))

        def _parse_deployed_via(
            data: object,
        ) -> (
            DeploymentResponseDeployedViaType1
            | DeploymentResponseDeployedViaType2Type1
            | DeploymentResponseDeployedViaType3Type1
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
                deployed_via_type_1 = check_deployment_response_deployed_via_type_1(data)

                return deployed_via_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                deployed_via_type_2_type_1 = check_deployment_response_deployed_via_type_2_type_1(data)

                return deployed_via_type_2_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                deployed_via_type_3_type_1 = check_deployment_response_deployed_via_type_3_type_1(data)

                return deployed_via_type_3_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(
                DeploymentResponseDeployedViaType1
                | DeploymentResponseDeployedViaType2Type1
                | DeploymentResponseDeployedViaType3Type1
                | None
                | Unset,
                data,
            )

        deployed_via = _parse_deployed_via(d.pop("deployed_via", UNSET))

        def _parse_deployed_from_ip(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        deployed_from_ip = _parse_deployed_from_ip(d.pop("deployed_from_ip", UNSET))

        def _parse_pusher_login(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        pusher_login = _parse_pusher_login(d.pop("pusher_login", UNSET))

        deployment_response = cls(
            id=id,
            app_id=app_id,
            image_digest=image_digest,
            kind=kind,
            status=status,
            created_at=created_at,
            build_id=build_id,
            error=error,
            error_code=error_code,
            error_hint=error_hint,
            error_why=error_why,
            error_fix=error_fix,
            error_relevant_logs=error_relevant_logs,
            has_overrides=has_overrides,
            override_entrypoint=override_entrypoint,
            override_cmd=override_cmd,
            override_env_keys=override_env_keys,
            override_env_secret_keys=override_env_secret_keys,
            override_env_secret_refs=override_env_secret_refs,
            override_port=override_port,
            override_healthcheck=override_healthcheck,
            override_liveness_probe=override_liveness_probe,
            min_instances=min_instances,
            scan=scan,
            parked_reason=parked_reason,
            parked_at=parked_at,
            traffic_percent=traffic_percent,
            scope=scope,
            secret_scan=secret_scan,
            build_plan=build_plan,
            deployed_by_user_id=deployed_by_user_id,
            deployed_via=deployed_via,
            deployed_from_ip=deployed_from_ip,
            pusher_login=pusher_login,
        )

        deployment_response.additional_properties = d
        return deployment_response

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
