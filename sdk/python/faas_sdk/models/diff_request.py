from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_manifest import AppManifest
    from ..models.create_cron_request import CreateCronRequest
    from ..models.create_edge_rule_request import CreateEdgeRuleRequest
    from ..models.diff_app_config_patch import DiffAppConfigPatch
    from ..models.diff_request_env_by_scope import DiffRequestEnvByScope


T = TypeVar("T", bound="DiffRequest")


@_attrs_define
class DiffRequest:
    """JSON body for POST /v1/apps/{slug}/diff (PR-1). Slim
    purpose-built DTO — every field maps 1:1 to a
    [deploydiff.Pending] entry via the apid handler's adapter.
    Empty / absent fields mean "no change proposed" (matches
    the engine's pointer semantics: every nested field is
    optional; null = "don't touch").

    """

    app_config: DiffAppConfigPatch | Unset = UNSET
    """Per-app scalar patch. Pointer-aware: nil = "don't touch";
    explicit zero / explicit value = "set to this". Matches
    [UpdateAppRequest] semantics but exposes only the fields
    the engine computes against (no ScalingPolicy /
    PublicAuth / OverflowNode).
    """
    image: str | Unset = UNSET
    """Would-write image reference. Empty = no image deploy
    proposed. Compared against the baseline's
    DeploymentResponse.ImageDigest for the immutable-diff
    check.
    """
    manifest: AppManifest | Unset = UNSET
    """App manifest: environment variables, build commands, working directory, healthcheck, user, and Dockerfile-
    as-source flag (§ux 6.3). The optional `env_secrets` field carries sealed-secret refs ("secret:NAME" strings)
    resolved by the host at wake time against the app_secrets table (issue #460 / ADR-053 §Decision 1). Values are
    NEVER sealed ciphertext — only refs. M-1 (ADR-136) widens the contract additively with `healthcheck`,
    `stop_signal`, `stop_grace_period` from the OCI image-config spec; old guest-init ignores unknown fields per
    JSON semantics, so the widen is wire-compatible. M-2 (ADR-137 + ADR-138) widens additively with
    `execution_mode`, `restart_policy`, `startup_deadline_s`, `max_retries`, and `service_replicas` — these govern
    the lifecycle contract (request vs service vs worker vs job) and the per-mode replica scaffold. Defaults
    preserve today's behaviour (execution_mode=request, restart_policy=on-failure)."""
    env_by_scope: DiffRequestEnvByScope | Unset = UNSET
    """Per-scope env write. Full-replacement semantics per
    scope (ADR-090 D3). Keys are scope names ("default",
    "staging", ...); values are DiffEnvRow arrays.
    """
    crons: list[CreateCronRequest] | Unset = UNSET
    edge_rules: list[CreateEdgeRuleRequest] | Unset = UNSET
    scope: None | str | Unset = UNSET
    """Pending per-deployment env scope (ADR-091 / SAFE-RELEASES production-leveling Stream E). Compared against
    Baseline.LatestScope; mismatch emits a scope_mismatch break. Empty = default."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_config: dict[str, Any] | Unset = UNSET
        if not isinstance(self.app_config, Unset):
            app_config = self.app_config.to_dict()

        image = self.image

        manifest: dict[str, Any] | Unset = UNSET
        if not isinstance(self.manifest, Unset):
            manifest = self.manifest.to_dict()

        env_by_scope: dict[str, Any] | Unset = UNSET
        if not isinstance(self.env_by_scope, Unset):
            env_by_scope = self.env_by_scope.to_dict()

        crons: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.crons, Unset):
            crons = []
            for crons_item_data in self.crons:
                crons_item = crons_item_data.to_dict()
                crons.append(crons_item)

        edge_rules: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.edge_rules, Unset):
            edge_rules = []
            for edge_rules_item_data in self.edge_rules:
                edge_rules_item = edge_rules_item_data.to_dict()
                edge_rules.append(edge_rules_item)

        scope: None | str | Unset
        if isinstance(self.scope, Unset):
            scope = UNSET
        else:
            scope = self.scope

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if app_config is not UNSET:
            field_dict["app_config"] = app_config
        if image is not UNSET:
            field_dict["image"] = image
        if manifest is not UNSET:
            field_dict["manifest"] = manifest
        if env_by_scope is not UNSET:
            field_dict["env_by_scope"] = env_by_scope
        if crons is not UNSET:
            field_dict["crons"] = crons
        if edge_rules is not UNSET:
            field_dict["edge_rules"] = edge_rules
        if scope is not UNSET:
            field_dict["scope"] = scope

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_manifest import AppManifest
        from ..models.create_cron_request import CreateCronRequest
        from ..models.create_edge_rule_request import CreateEdgeRuleRequest
        from ..models.diff_app_config_patch import DiffAppConfigPatch
        from ..models.diff_request_env_by_scope import DiffRequestEnvByScope

        d = dict(src_dict)
        _app_config = d.pop("app_config", UNSET)
        app_config: DiffAppConfigPatch | Unset
        if isinstance(_app_config, Unset):
            app_config = UNSET
        else:
            app_config = DiffAppConfigPatch.from_dict(_app_config)

        image = d.pop("image", UNSET)

        _manifest = d.pop("manifest", UNSET)
        manifest: AppManifest | Unset
        if isinstance(_manifest, Unset):
            manifest = UNSET
        else:
            manifest = AppManifest.from_dict(_manifest)

        _env_by_scope = d.pop("env_by_scope", UNSET)
        env_by_scope: DiffRequestEnvByScope | Unset
        if isinstance(_env_by_scope, Unset):
            env_by_scope = UNSET
        else:
            env_by_scope = DiffRequestEnvByScope.from_dict(_env_by_scope)

        _crons = d.pop("crons", UNSET)
        crons: list[CreateCronRequest] | Unset = UNSET
        if _crons is not UNSET:
            crons = []
            for crons_item_data in _crons:
                crons_item = CreateCronRequest.from_dict(crons_item_data)

                crons.append(crons_item)

        _edge_rules = d.pop("edge_rules", UNSET)
        edge_rules: list[CreateEdgeRuleRequest] | Unset = UNSET
        if _edge_rules is not UNSET:
            edge_rules = []
            for edge_rules_item_data in _edge_rules:
                edge_rules_item = CreateEdgeRuleRequest.from_dict(edge_rules_item_data)

                edge_rules.append(edge_rules_item)

        def _parse_scope(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        scope = _parse_scope(d.pop("scope", UNSET))

        diff_request = cls(
            app_config=app_config,
            image=image,
            manifest=manifest,
            env_by_scope=env_by_scope,
            crons=crons,
            edge_rules=edge_rules,
            scope=scope,
        )

        diff_request.additional_properties = d
        return diff_request

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
