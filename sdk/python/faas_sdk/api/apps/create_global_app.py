from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_global_app_request import CreateGlobalAppRequest
from ...models.global_app_response import GlobalAppResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: CreateGlobalAppRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/global-apps",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> GlobalAppResponse | Problem | None:
    if response.status_code == 201:
        response_201 = GlobalAppResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[GlobalAppResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateGlobalAppRequest,
) -> Response[GlobalAppResponse | Problem]:
    """Register a global app identity.

     Requires personal account deploy-write access and verified email. Idempotent for the same account
    and slug. Uses the plan app-count limit for directory registrations. Creates no regional app or
    runtime resource entitlement. Initial serving policy is all selected regions.

    Args:
        body (CreateGlobalAppRequest): Global identity registration with no regional provisioning.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GlobalAppResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: CreateGlobalAppRequest,
) -> GlobalAppResponse | Problem | None:
    """Register a global app identity.

     Requires personal account deploy-write access and verified email. Idempotent for the same account
    and slug. Uses the plan app-count limit for directory registrations. Creates no regional app or
    runtime resource entitlement. Initial serving policy is all selected regions.

    Args:
        body (CreateGlobalAppRequest): Global identity registration with no regional provisioning.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GlobalAppResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateGlobalAppRequest,
) -> Response[GlobalAppResponse | Problem]:
    """Register a global app identity.

     Requires personal account deploy-write access and verified email. Idempotent for the same account
    and slug. Uses the plan app-count limit for directory registrations. Creates no regional app or
    runtime resource entitlement. Initial serving policy is all selected regions.

    Args:
        body (CreateGlobalAppRequest): Global identity registration with no regional provisioning.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GlobalAppResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CreateGlobalAppRequest,
) -> GlobalAppResponse | Problem | None:
    """Register a global app identity.

     Requires personal account deploy-write access and verified email. Idempotent for the same account
    and slug. Uses the plan app-count limit for directory registrations. Creates no regional app or
    runtime resource entitlement. Initial serving policy is all selected regions.

    Args:
        body (CreateGlobalAppRequest): Global identity registration with no regional provisioning.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GlobalAppResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
