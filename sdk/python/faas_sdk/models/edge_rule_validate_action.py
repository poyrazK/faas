from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.edge_rule_validate_action_validate_mode import (
    EdgeRuleValidateActionValidateMode,
    check_edge_rule_validate_action_validate_mode,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.edge_rule_validate_action_schema import EdgeRuleValidateActionSchema
    from ..models.edge_rule_validate_parameters import EdgeRuleValidateParameters


T = TypeVar("T", bound="EdgeRuleValidateAction")


@_attrs_define
class EdgeRuleValidateAction:
    """Customer-supplied JSON Schema (Draft 2020-12) evaluated against
    the inbound request body BEFORE the wake gate fires. The
    kind=validate rule is the platform's API-native request-
    validation surface: rejections return 422
    `request_validation_failed` with `Problem.errors` carrying
    per-field detail, never pay a cold-boot cost, never consume
    the rate-limit / wake-quota budget on malformed traffic.

    Free-and-above (no plan gate). Schema lives inline in the
    `action` jsonb blob (single-table flow), capped at the
    platform's `MaxEdgeRuleValidateSchemaBytes` (64 KiB). The
    gateway re-validates at compile time as defence-in-depth so
    the SQL hotfix path that bypasses apid-Validate still cannot
    ship an external `$ref`.

    Field-by-field:
      * `schema` — JSON Schema document (Draft 2020-12) for the
        request body. Capped at 64 KiB. External `$ref` / `$id` URLs
        are rejected at create-time; internal pointers
        (`#/definitions/Foo`) pass through. Optional when
        `parameters` is set, so a body-less request can be validated.
      * `parameters` — optional path, query, and header schemas,
        checked before the body is read (ADR-091 amendment).
      * `content_types` — optional media-type allowlist.
        Closed set `application/*`. Empty = match any.
      * `apply_while_streaming` — per-rule opt-in for the
        streaming response path (ADR-047). Default false mirrors
        the §4.1 `Accept: application/json` opt-out.
      * `reject_on_unknown_fields` — toggles
        `additionalProperties: false` on the compiled schema.
        Default false preserves byte-stable schemas.
      * `max_body_bytes` — per-rule inbound body cap. 0 =
        inherit `MaxRequestBodyBytes` (per-plan 25 MB buffered /
        100 MB streaming). Must be > 0 and <= `MaxRequestBodyBytes`.
    At least one of `schema` and `parameters` is required.

    """

    schema: EdgeRuleValidateActionSchema | Unset = UNSET
    """Inline JSON Schema document (Draft 2020-12). The schema
    is preserved byte-exact across apid↔gatewayd round-trips
    so the SHA-256 cache key in `pkg/edgevalidate` is stable.
    """
    parameters: EdgeRuleValidateParameters | Unset = UNSET
    """Path, query, and header validation for a kind=validate rule
    (ADR-091 amendment: request parameters). Each schema is a JSON
    Schema object whose properties are parameter names; property types
    must be string, integer, number, boolean, or an array of those.
    Request values are converted to the declared type before
    validation, and a value that does not convert stays a string so
    the schema reports it. All three are checked before the body is
    read, in the rule's validate_mode; errors name the location
    (`path/id`, `query/limit`, `headers/x-tenant`).
    """
    content_types: list[str] | Unset = UNSET
    """Optional media-type allowlist. Every entry must start
    with `application/`. Empty array = match any Content-Type.
    """
    apply_while_streaming: bool | Unset = UNSET
    """Whether validation fires on the streaming response path
    (ADR-047). Default false; set true per-rule to opt the
    SSE / chunked response path into body validation.
    """
    reject_on_unknown_fields: bool | Unset = UNSET
    """Toggles `additionalProperties: false` on the compiled
    schema. Default false so a body with stray fields does
    not silently fail; opt in per-rule for strict schemas.
    """
    max_body_bytes: int | Unset = UNSET
    """Per-rule inbound body cap. 0 (default) inherits the
    platform cap (`api.MaxRequestBodyBytes`). When set, must
    be > 0 and <= the platform cap.
    """
    validate_mode: EdgeRuleValidateActionValidateMode | Unset = "block"
    """DEPRECATED (ADR-128 D2). Moved to top-level
    `EdgeRuleResponse.validate_mode` /
    `CreateEdgeRuleRequest.validate_mode` /
    `UpdateEdgeRuleRequest.validate_mode`. Retained on
    the action body for the back-compat read window.
    How the gateway handles a schema mismatch. `block`
    rejects with 422 (the strictest mode; preserves the
    pre-2026 behaviour). `observe` counts via the metric
    and proxies normally. `warn` does the same and stamps
    `X-Validation-Warning: <rule_id>` on the response.
    An empty / omitted value is coerced to `block` at the
    gateway-side handler.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        schema: dict[str, Any] | Unset = UNSET
        if not isinstance(self.schema, Unset):
            schema = self.schema.to_dict()

        parameters: dict[str, Any] | Unset = UNSET
        if not isinstance(self.parameters, Unset):
            parameters = self.parameters.to_dict()

        content_types: list[str] | Unset = UNSET
        if not isinstance(self.content_types, Unset):
            content_types = self.content_types

        apply_while_streaming = self.apply_while_streaming

        reject_on_unknown_fields = self.reject_on_unknown_fields

        max_body_bytes = self.max_body_bytes

        validate_mode: str | Unset = UNSET
        if not isinstance(self.validate_mode, Unset):
            validate_mode = self.validate_mode

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if schema is not UNSET:
            field_dict["schema"] = schema
        if parameters is not UNSET:
            field_dict["parameters"] = parameters
        if content_types is not UNSET:
            field_dict["content_types"] = content_types
        if apply_while_streaming is not UNSET:
            field_dict["apply_while_streaming"] = apply_while_streaming
        if reject_on_unknown_fields is not UNSET:
            field_dict["reject_on_unknown_fields"] = reject_on_unknown_fields
        if max_body_bytes is not UNSET:
            field_dict["max_body_bytes"] = max_body_bytes
        if validate_mode is not UNSET:
            field_dict["validate_mode"] = validate_mode

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_validate_action_schema import EdgeRuleValidateActionSchema
        from ..models.edge_rule_validate_parameters import EdgeRuleValidateParameters

        d = dict(src_dict)
        _schema = d.pop("schema", UNSET)
        schema: EdgeRuleValidateActionSchema | Unset
        if isinstance(_schema, Unset):
            schema = UNSET
        else:
            schema = EdgeRuleValidateActionSchema.from_dict(_schema)

        _parameters = d.pop("parameters", UNSET)
        parameters: EdgeRuleValidateParameters | Unset
        if isinstance(_parameters, Unset):
            parameters = UNSET
        else:
            parameters = EdgeRuleValidateParameters.from_dict(_parameters)

        content_types = cast(list[str], d.pop("content_types", UNSET))

        apply_while_streaming = d.pop("apply_while_streaming", UNSET)

        reject_on_unknown_fields = d.pop("reject_on_unknown_fields", UNSET)

        max_body_bytes = d.pop("max_body_bytes", UNSET)

        _validate_mode = d.pop("validate_mode", UNSET)
        validate_mode: EdgeRuleValidateActionValidateMode | Unset
        if isinstance(_validate_mode, Unset):
            validate_mode = UNSET
        else:
            validate_mode = check_edge_rule_validate_action_validate_mode(_validate_mode)

        edge_rule_validate_action = cls(
            schema=schema,
            parameters=parameters,
            content_types=content_types,
            apply_while_streaming=apply_while_streaming,
            reject_on_unknown_fields=reject_on_unknown_fields,
            max_body_bytes=max_body_bytes,
            validate_mode=validate_mode,
        )

        edge_rule_validate_action.additional_properties = d
        return edge_rule_validate_action

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
