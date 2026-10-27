from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.binding_promotion_request import BindingPromotionRequest
from ...models.binding_promotion_response import BindingPromotionResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: BindingPromotionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/deployments/{id}/promote".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> BindingPromotionResponse | Problem | None:
    if response.status_code == 200:
        response_200 = BindingPromotionResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 500:
        response_500 = Problem.from_dict(response.json())

        return response_500

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[BindingPromotionResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: BindingPromotionRequest,
) -> Response[BindingPromotionResponse | Problem]:
    """Promote a deployment after an atomic bindings check.

     Available on all supported plans. Evaluate the exact live, materialized candidate using
    the bindings preflight policy, then compare its binding/configuration,
    probe and runtime revision inside the traffic transaction. Blockers,
    expired evidence or changed observations return 409 without changing
    traffic. No probes or restarts are scheduled. Queue/outbound probe
    coverage requires an explicit allow_unsupported waiver and remains
    partial. A successful receipt confirms the applied policy and check.
    An optional serving expectation is checked under the traffic locks.
    An already promoted target still requires a passed bindings check;
    it returns an idempotent receipt without requiring the previous
    deployment to remain at 100%. Active managed canaries cannot be bypassed.
    This dedicated route prevents older servers from ignoring the bindings gate.
    For require_application_ack use promote-with-application-ack so an older
    server cannot silently ignore the new policy field.

    Args:
        id (str):
        body (BindingPromotionRequest): Policy for a server-enforced bindings promotion; the gate
            is always required on this route. Stored scope policy may require a shorter age or
            application ACKs and disallow unsupported waivers. The response reports the effective
            policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BindingPromotionResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: BindingPromotionRequest,
) -> BindingPromotionResponse | Problem | None:
    """Promote a deployment after an atomic bindings check.

     Available on all supported plans. Evaluate the exact live, materialized candidate using
    the bindings preflight policy, then compare its binding/configuration,
    probe and runtime revision inside the traffic transaction. Blockers,
    expired evidence or changed observations return 409 without changing
    traffic. No probes or restarts are scheduled. Queue/outbound probe
    coverage requires an explicit allow_unsupported waiver and remains
    partial. A successful receipt confirms the applied policy and check.
    An optional serving expectation is checked under the traffic locks.
    An already promoted target still requires a passed bindings check;
    it returns an idempotent receipt without requiring the previous
    deployment to remain at 100%. Active managed canaries cannot be bypassed.
    This dedicated route prevents older servers from ignoring the bindings gate.
    For require_application_ack use promote-with-application-ack so an older
    server cannot silently ignore the new policy field.

    Args:
        id (str):
        body (BindingPromotionRequest): Policy for a server-enforced bindings promotion; the gate
            is always required on this route. Stored scope policy may require a shorter age or
            application ACKs and disallow unsupported waivers. The response reports the effective
            policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BindingPromotionResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: BindingPromotionRequest,
) -> Response[BindingPromotionResponse | Problem]:
    """Promote a deployment after an atomic bindings check.

     Available on all supported plans. Evaluate the exact live, materialized candidate using
    the bindings preflight policy, then compare its binding/configuration,
    probe and runtime revision inside the traffic transaction. Blockers,
    expired evidence or changed observations return 409 without changing
    traffic. No probes or restarts are scheduled. Queue/outbound probe
    coverage requires an explicit allow_unsupported waiver and remains
    partial. A successful receipt confirms the applied policy and check.
    An optional serving expectation is checked under the traffic locks.
    An already promoted target still requires a passed bindings check;
    it returns an idempotent receipt without requiring the previous
    deployment to remain at 100%. Active managed canaries cannot be bypassed.
    This dedicated route prevents older servers from ignoring the bindings gate.
    For require_application_ack use promote-with-application-ack so an older
    server cannot silently ignore the new policy field.

    Args:
        id (str):
        body (BindingPromotionRequest): Policy for a server-enforced bindings promotion; the gate
            is always required on this route. Stored scope policy may require a shorter age or
            application ACKs and disallow unsupported waivers. The response reports the effective
            policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BindingPromotionResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: BindingPromotionRequest,
) -> BindingPromotionResponse | Problem | None:
    """Promote a deployment after an atomic bindings check.

     Available on all supported plans. Evaluate the exact live, materialized candidate using
    the bindings preflight policy, then compare its binding/configuration,
    probe and runtime revision inside the traffic transaction. Blockers,
    expired evidence or changed observations return 409 without changing
    traffic. No probes or restarts are scheduled. Queue/outbound probe
    coverage requires an explicit allow_unsupported waiver and remains
    partial. A successful receipt confirms the applied policy and check.
    An optional serving expectation is checked under the traffic locks.
    An already promoted target still requires a passed bindings check;
    it returns an idempotent receipt without requiring the previous
    deployment to remain at 100%. Active managed canaries cannot be bypassed.
    This dedicated route prevents older servers from ignoring the bindings gate.
    For require_application_ack use promote-with-application-ack so an older
    server cannot silently ignore the new policy field.

    Args:
        id (str):
        body (BindingPromotionRequest): Policy for a server-enforced bindings promotion; the gate
            is always required on this route. Stored scope policy may require a shorter age or
            application ACKs and disallow unsupported waivers. The response reports the effective
            policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BindingPromotionResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
