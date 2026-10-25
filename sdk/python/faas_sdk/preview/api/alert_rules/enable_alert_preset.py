from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.alert_rule_response import AlertRuleResponse
from ...models.enable_alert_preset_request import EnableAlertPresetRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    name: str,
    *,
    body: EnableAlertPresetRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/alert-presets/{name}/enable".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AlertRuleResponse | Problem | None:
    if response.status_code == 201:
        response_201 = AlertRuleResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AlertRuleResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: EnableAlertPresetRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[AlertRuleResponse | Problem]:
    """Instantiate a preset as an alert rule.

     Clones the catalog row into a real alert_rules row the
    caller owns from then on. The (metric, comparison,
    threshold, window_spec, default_cooldown_minutes)
    sextuple is pre-filled server-side; the caller supplies
    webhook_url + webhook_secret (the delivery channel), an
    optional safe-release action, and cooldown_minutes / enabled
    overrides.

    Pre-loadApp gates fire in this order: 404 on missing
    preset → 400 alert_preset_disabled on disabled-in-catalog
    → 402 plan_alert_presets_not_allowed on below-minimum-plan
    → 400 alert_preset_invalid on body shape → 400
    image_egress_denied on the SSRF egress guard → 402
    plan_alert_rules_not_allowed on the per-plan cap → 403
    plan_alert_rule_quota on the per-app / per-account cap.

    Args:
        slug (str):
        name (str):
        idempotency_key (str | Unset):
        body (EnableAlertPresetRequest): Body for POST /v1/apps/{slug}/alert-
            presets/{name}/enable.
            The (name, metric, comparison, threshold, window_spec,
            default_cooldown_minutes) sextuple is pre-filled from the
            catalog; the caller supplies the delivery-side fields and an
            optional safe-release action.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AlertRuleResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: EnableAlertPresetRequest,
    idempotency_key: str | Unset = UNSET,
) -> AlertRuleResponse | Problem | None:
    """Instantiate a preset as an alert rule.

     Clones the catalog row into a real alert_rules row the
    caller owns from then on. The (metric, comparison,
    threshold, window_spec, default_cooldown_minutes)
    sextuple is pre-filled server-side; the caller supplies
    webhook_url + webhook_secret (the delivery channel), an
    optional safe-release action, and cooldown_minutes / enabled
    overrides.

    Pre-loadApp gates fire in this order: 404 on missing
    preset → 400 alert_preset_disabled on disabled-in-catalog
    → 402 plan_alert_presets_not_allowed on below-minimum-plan
    → 400 alert_preset_invalid on body shape → 400
    image_egress_denied on the SSRF egress guard → 402
    plan_alert_rules_not_allowed on the per-plan cap → 403
    plan_alert_rule_quota on the per-app / per-account cap.

    Args:
        slug (str):
        name (str):
        idempotency_key (str | Unset):
        body (EnableAlertPresetRequest): Body for POST /v1/apps/{slug}/alert-
            presets/{name}/enable.
            The (name, metric, comparison, threshold, window_spec,
            default_cooldown_minutes) sextuple is pre-filled from the
            catalog; the caller supplies the delivery-side fields and an
            optional safe-release action.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AlertRuleResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: EnableAlertPresetRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[AlertRuleResponse | Problem]:
    """Instantiate a preset as an alert rule.

     Clones the catalog row into a real alert_rules row the
    caller owns from then on. The (metric, comparison,
    threshold, window_spec, default_cooldown_minutes)
    sextuple is pre-filled server-side; the caller supplies
    webhook_url + webhook_secret (the delivery channel), an
    optional safe-release action, and cooldown_minutes / enabled
    overrides.

    Pre-loadApp gates fire in this order: 404 on missing
    preset → 400 alert_preset_disabled on disabled-in-catalog
    → 402 plan_alert_presets_not_allowed on below-minimum-plan
    → 400 alert_preset_invalid on body shape → 400
    image_egress_denied on the SSRF egress guard → 402
    plan_alert_rules_not_allowed on the per-plan cap → 403
    plan_alert_rule_quota on the per-app / per-account cap.

    Args:
        slug (str):
        name (str):
        idempotency_key (str | Unset):
        body (EnableAlertPresetRequest): Body for POST /v1/apps/{slug}/alert-
            presets/{name}/enable.
            The (name, metric, comparison, threshold, window_spec,
            default_cooldown_minutes) sextuple is pre-filled from the
            catalog; the caller supplies the delivery-side fields and an
            optional safe-release action.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AlertRuleResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: EnableAlertPresetRequest,
    idempotency_key: str | Unset = UNSET,
) -> AlertRuleResponse | Problem | None:
    """Instantiate a preset as an alert rule.

     Clones the catalog row into a real alert_rules row the
    caller owns from then on. The (metric, comparison,
    threshold, window_spec, default_cooldown_minutes)
    sextuple is pre-filled server-side; the caller supplies
    webhook_url + webhook_secret (the delivery channel), an
    optional safe-release action, and cooldown_minutes / enabled
    overrides.

    Pre-loadApp gates fire in this order: 404 on missing
    preset → 400 alert_preset_disabled on disabled-in-catalog
    → 402 plan_alert_presets_not_allowed on below-minimum-plan
    → 400 alert_preset_invalid on body shape → 400
    image_egress_denied on the SSRF egress guard → 402
    plan_alert_rules_not_allowed on the per-plan cap → 403
    plan_alert_rule_quota on the per-app / per-account cap.

    Args:
        slug (str):
        name (str):
        idempotency_key (str | Unset):
        body (EnableAlertPresetRequest): Body for POST /v1/apps/{slug}/alert-
            presets/{name}/enable.
            The (name, metric, comparison, threshold, window_spec,
            default_cooldown_minutes) sextuple is pre-filled from the
            catalog; the caller supplies the delivery-side fields and an
            optional safe-release action.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AlertRuleResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
