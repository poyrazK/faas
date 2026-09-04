from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.field_error import FieldError
    from ..models.log_excerpt import LogExcerpt
    from ..models.secret_finding import SecretFinding


T = TypeVar("T", bound="Problem")


@_attrs_define
class Problem:
    """RFC 7807 problem+json envelope. The `code` field is the stable
    machine-readable identifier; clients branch on it. `limit` and
    `observed` are populated on quota errors. `docs_url` points the
    user at the next action. `billing_portal_url` is populated on
    `code: payment_required` when the customer already has a
    provider subscription and must update it in the provider
    portal. `checkout_url` is populated when a new hosted checkout
    is required. `paddle_checkout_url` is retained as a legacy
    alias for Paddle clients, and `tx_id` carries the provider
    checkout handle when one exists.

    `errors` carries per-field detail (Cloudflare / Stripe shape)
    for 422 sites that emit a list of field-level failures — used
    today by the kind=validate edge rule so a JSON Schema
    rejection renders as a form-field list the dashboard can
    iterate without parsing prose. Optional + omitempty so every
    other problem+json site keeps its existing flat shape unchanged.

        Example:
            {'type': 'https://docs.gregale.dev/errors/validation_failed', 'title': 'Validation failed', 'status': 422,
                'code': 'validation_failed', 'detail': 'ram_mb must be one of [128, 256, 512, 1024, 2048]', 'limit': None,
                'observed': None, 'docs_url': 'https://docs.gregale.dev/errors/validation_failed'}

    """

    title: str
    status: int
    code: str
    """Stable machine-readable error code. See StatusForCode in pkg/api/errors.go."""
    type_: str | Unset = UNSET
    detail: str | Unset = UNSET
    limit: int | None | Unset = UNSET
    observed: int | None | Unset = UNSET
    docs_url: str | Unset = UNSET
    checkout_url: str | Unset = UNSET
    """Provider-neutral hosted checkout URL on a `payment_required`
    402 when a paid plan upgrade requires a new subscription.
    """
    billing_portal_url: str | Unset = UNSET
    paddle_checkout_url: str | Unset = UNSET
    """Legacy Paddle-hosted checkout URL on a `payment_required`
    402. Prefer the provider-neutral `checkout_url` field.
    """
    tx_id: str | Unset = UNSET
    """Paddle transaction handle (`txn_…`) on a `payment_required`
    402. Empty on the Stripe path. The dashboard renders this as
    a confirmation id after the customer completes checkout.
    """
    errors: list[FieldError] | Unset = UNSET
    """Per-field validation detail. Populated by 422 sites that
    emit a list of field-level failures. Each entry is a
    `FieldError` (Cloudflare / Stripe shape: field + expected
    + got) so an SDK can drive form-field UI without parsing
    prose.
    """
    secret_findings: list[SecretFinding] | Unset = UNSET
    """Per-line secret-scan detail. Populated by 422 sites with
    `code: secret_scan_strict` (cmd/apid/secretscan.go
    server-side scan rejection; cmd/gregale printErr
    --secret-scan=strict client-side rejection). The shape
    is shared with the on-disk `SecretScanResult` so a
    programmatic consumer can render the same UI for both
    rejection paths. Optional + omitempty.
    """
    secret_hint: str | Unset = UNSET
    """Customer-facing remediation nudge attached to a
    `code: secret_scan_strict` 422 envelope (e.g. "move
    detected secrets to `gregale secrets set`"). Mirrors
    the `FieldError` shape's prose pattern so the dashboard
    / SDK can render the hint as a one-line footer without
    parsing prose. Optional + omitempty.
    """
    hint: str | Unset = UNSET
    """Single short next-action line lifted from the
    `pkg/whycopy` catalog (error-explanations cluster,
    spec §6.4 amendment 1). Populated by the 9 cluster-
    owned RFC 7807 codes (app_not_listening,
    app_loopback_bound, app_arch_mismatch,
    env_var_missing, app_healthz_unauthorized,
    app_runtime_oom, dep_install_failed,
    app_startup_timeout, stateless_only_violation). The
    CLI renders this as the first line of the 5-line
    error shape (`hint: <hint>`). Optional + omitempty
    so every other problem+json site keeps its existing
    3-line shape unchanged.
    """
    why: str | Unset = UNSET
    """Human-readable cause with the observed value templated
    in (error-explanations cluster, spec §6.4 amendment 1).
    Distinct from `detail`: `detail` is the platform's
    machine-stable message; `why` is the customer-facing
    explanation. Multi-line (≤512 bytes per `pkg/whycopy`
    catalog row). Optional + omitempty.
    """
    fix: str | Unset = UNSET
    """Prescriptive remediation (1-3 lines, error-explanations
    cluster, spec §6.4 amendment 1). Distinct from `hint`:
    `hint` is a single line, `fix` is the bulleted
    remediation list. The CLI renders this as
    `→ fix: <fix>` with literal newlines preserved so the
    multi-line shape survives. Optional + omitempty.
    """
    relevant_logs: list[LogExcerpt] | Unset = UNSET
    """Per-line log excerpts that explain the failure (error-
    explanations cluster, spec §6.4 amendment 1). The
    detection site attaches the last N log lines that
    caused the failure (capped at 20 entries × 512 bytes
    each per CLI tripwire). The CLI renders the first 5
    inline as a fenced block. Optional + omitempty.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        title = self.title

        status = self.status

        code = self.code

        type_ = self.type_

        detail = self.detail

        limit: int | None | Unset
        if isinstance(self.limit, Unset):
            limit = UNSET
        else:
            limit = self.limit

        observed: int | None | Unset
        if isinstance(self.observed, Unset):
            observed = UNSET
        else:
            observed = self.observed

        docs_url = self.docs_url

        checkout_url = self.checkout_url

        billing_portal_url = self.billing_portal_url

        paddle_checkout_url = self.paddle_checkout_url

        tx_id = self.tx_id

        errors: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.errors, Unset):
            errors = []
            for errors_item_data in self.errors:
                errors_item = errors_item_data.to_dict()
                errors.append(errors_item)

        secret_findings: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.secret_findings, Unset):
            secret_findings = []
            for secret_findings_item_data in self.secret_findings:
                secret_findings_item = secret_findings_item_data.to_dict()
                secret_findings.append(secret_findings_item)

        secret_hint = self.secret_hint

        hint = self.hint

        why = self.why

        fix = self.fix

        relevant_logs: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.relevant_logs, Unset):
            relevant_logs = []
            for relevant_logs_item_data in self.relevant_logs:
                relevant_logs_item = relevant_logs_item_data.to_dict()
                relevant_logs.append(relevant_logs_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "title": title,
                "status": status,
                "code": code,
            }
        )
        if type_ is not UNSET:
            field_dict["type"] = type_
        if detail is not UNSET:
            field_dict["detail"] = detail
        if limit is not UNSET:
            field_dict["limit"] = limit
        if observed is not UNSET:
            field_dict["observed"] = observed
        if docs_url is not UNSET:
            field_dict["docs_url"] = docs_url
        if checkout_url is not UNSET:
            field_dict["checkout_url"] = checkout_url
        if billing_portal_url is not UNSET:
            field_dict["billing_portal_url"] = billing_portal_url
        if paddle_checkout_url is not UNSET:
            field_dict["paddle_checkout_url"] = paddle_checkout_url
        if tx_id is not UNSET:
            field_dict["tx_id"] = tx_id
        if errors is not UNSET:
            field_dict["errors"] = errors
        if secret_findings is not UNSET:
            field_dict["secret_findings"] = secret_findings
        if secret_hint is not UNSET:
            field_dict["secret_hint"] = secret_hint
        if hint is not UNSET:
            field_dict["hint"] = hint
        if why is not UNSET:
            field_dict["why"] = why
        if fix is not UNSET:
            field_dict["fix"] = fix
        if relevant_logs is not UNSET:
            field_dict["relevant_logs"] = relevant_logs

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.field_error import FieldError
        from ..models.log_excerpt import LogExcerpt
        from ..models.secret_finding import SecretFinding

        d = dict(src_dict)
        title = d.pop("title")

        status = d.pop("status")

        code = d.pop("code")

        type_ = d.pop("type", UNSET)

        detail = d.pop("detail", UNSET)

        def _parse_limit(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        limit = _parse_limit(d.pop("limit", UNSET))

        def _parse_observed(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        observed = _parse_observed(d.pop("observed", UNSET))

        docs_url = d.pop("docs_url", UNSET)

        checkout_url = d.pop("checkout_url", UNSET)

        billing_portal_url = d.pop("billing_portal_url", UNSET)

        paddle_checkout_url = d.pop("paddle_checkout_url", UNSET)

        tx_id = d.pop("tx_id", UNSET)

        _errors = d.pop("errors", UNSET)
        errors: list[FieldError] | Unset = UNSET
        if _errors is not UNSET:
            errors = []
            for errors_item_data in _errors:
                errors_item = FieldError.from_dict(errors_item_data)

                errors.append(errors_item)

        _secret_findings = d.pop("secret_findings", UNSET)
        secret_findings: list[SecretFinding] | Unset = UNSET
        if _secret_findings is not UNSET:
            secret_findings = []
            for secret_findings_item_data in _secret_findings:
                secret_findings_item = SecretFinding.from_dict(secret_findings_item_data)

                secret_findings.append(secret_findings_item)

        secret_hint = d.pop("secret_hint", UNSET)

        hint = d.pop("hint", UNSET)

        why = d.pop("why", UNSET)

        fix = d.pop("fix", UNSET)

        _relevant_logs = d.pop("relevant_logs", UNSET)
        relevant_logs: list[LogExcerpt] | Unset = UNSET
        if _relevant_logs is not UNSET:
            relevant_logs = []
            for relevant_logs_item_data in _relevant_logs:
                relevant_logs_item = LogExcerpt.from_dict(relevant_logs_item_data)

                relevant_logs.append(relevant_logs_item)

        problem = cls(
            title=title,
            status=status,
            code=code,
            type_=type_,
            detail=detail,
            limit=limit,
            observed=observed,
            docs_url=docs_url,
            checkout_url=checkout_url,
            billing_portal_url=billing_portal_url,
            paddle_checkout_url=paddle_checkout_url,
            tx_id=tx_id,
            errors=errors,
            secret_findings=secret_findings,
            secret_hint=secret_hint,
            hint=hint,
            why=why,
            fix=fix,
            relevant_logs=relevant_logs,
        )

        problem.additional_properties = d
        return problem

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
