from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deployment_response_check_capabilities import DeploymentResponseCheckCapabilities
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/deployments/response-check/capabilities",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeploymentResponseCheckCapabilities | Problem | None:
    if response.status_code == 200:
        response_200 = DeploymentResponseCheckCapabilities.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DeploymentResponseCheckCapabilities | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[DeploymentResponseCheckCapabilities | Problem]:
    """Read deployment response-check compatibility before deploying.

     Requires deployment-write access. This read-only preflight does not require
    MFA, resolve an app, allocate capacity, create work, or contact a guest.
    Queries the configured gateway control listener with a three-second bound
    and returns protocol support and forwarding configuration. An unavailable
    gateway is reported in a 200 response with available=false and a stable
    unavailable_reason: gateway_unsupported, gateway_unconfigured, or
    gateway_unavailable. Account credentials and internal addresses are not
    forwarded or returned. A successful preflight is not a capacity or future
    availability guarantee. Responses use Cache-Control: no-store.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponseCheckCapabilities | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> DeploymentResponseCheckCapabilities | Problem | None:
    """Read deployment response-check compatibility before deploying.

     Requires deployment-write access. This read-only preflight does not require
    MFA, resolve an app, allocate capacity, create work, or contact a guest.
    Queries the configured gateway control listener with a three-second bound
    and returns protocol support and forwarding configuration. An unavailable
    gateway is reported in a 200 response with available=false and a stable
    unavailable_reason: gateway_unsupported, gateway_unconfigured, or
    gateway_unavailable. Account credentials and internal addresses are not
    forwarded or returned. A successful preflight is not a capacity or future
    availability guarantee. Responses use Cache-Control: no-store.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponseCheckCapabilities | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[DeploymentResponseCheckCapabilities | Problem]:
    """Read deployment response-check compatibility before deploying.

     Requires deployment-write access. This read-only preflight does not require
    MFA, resolve an app, allocate capacity, create work, or contact a guest.
    Queries the configured gateway control listener with a three-second bound
    and returns protocol support and forwarding configuration. An unavailable
    gateway is reported in a 200 response with available=false and a stable
    unavailable_reason: gateway_unsupported, gateway_unconfigured, or
    gateway_unavailable. Account credentials and internal addresses are not
    forwarded or returned. A successful preflight is not a capacity or future
    availability guarantee. Responses use Cache-Control: no-store.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponseCheckCapabilities | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> DeploymentResponseCheckCapabilities | Problem | None:
    """Read deployment response-check compatibility before deploying.

     Requires deployment-write access. This read-only preflight does not require
    MFA, resolve an app, allocate capacity, create work, or contact a guest.
    Queries the configured gateway control listener with a three-second bound
    and returns protocol support and forwarding configuration. An unavailable
    gateway is reported in a 200 response with available=false and a stable
    unavailable_reason: gateway_unsupported, gateway_unconfigured, or
    gateway_unavailable. Account credentials and internal addresses are not
    forwarded or returned. A successful preflight is not a capacity or future
    availability guarantee. Responses use Cache-Control: no-store.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponseCheckCapabilities | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
