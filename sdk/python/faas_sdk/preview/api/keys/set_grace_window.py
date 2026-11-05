from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.grace_window_response import GraceWindowResponse
from ...models.problem import Problem
from ...models.set_grace_window_request import SetGraceWindowRequest
from ...types import Response


def _get_kwargs(
    *,
    body: SetGraceWindowRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/account/keys/grace_window_days",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> GraceWindowResponse | Problem | None:
    if response.status_code == 200:
        response_200 = GraceWindowResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[GraceWindowResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: SetGraceWindowRequest,
) -> Response[GraceWindowResponse | Problem]:
    """Set the per-account rotation grace-window override.

     Writes the override and invalidates the in-process cache so
    the next rotation observes the new value. `days=0` is atomic
    rotation (no grace); `days=null` (or omission) clears the
    override and falls back to the plan default.

    Args:
        body (SetGraceWindowRequest): Body of PATCH /v1/account/keys/grace_window_days. `days` is
            the per-account override for the rotation grace window. `days=0` is atomic rotation;
            `days=null` (or omitted) clears the override and falls back to the plan default
            (api.DefaultAPIKeyGraceWindowDays = 7).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GraceWindowResponse | Problem]
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
    body: SetGraceWindowRequest,
) -> GraceWindowResponse | Problem | None:
    """Set the per-account rotation grace-window override.

     Writes the override and invalidates the in-process cache so
    the next rotation observes the new value. `days=0` is atomic
    rotation (no grace); `days=null` (or omission) clears the
    override and falls back to the plan default.

    Args:
        body (SetGraceWindowRequest): Body of PATCH /v1/account/keys/grace_window_days. `days` is
            the per-account override for the rotation grace window. `days=0` is atomic rotation;
            `days=null` (or omitted) clears the override and falls back to the plan default
            (api.DefaultAPIKeyGraceWindowDays = 7).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GraceWindowResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: SetGraceWindowRequest,
) -> Response[GraceWindowResponse | Problem]:
    """Set the per-account rotation grace-window override.

     Writes the override and invalidates the in-process cache so
    the next rotation observes the new value. `days=0` is atomic
    rotation (no grace); `days=null` (or omission) clears the
    override and falls back to the plan default.

    Args:
        body (SetGraceWindowRequest): Body of PATCH /v1/account/keys/grace_window_days. `days` is
            the per-account override for the rotation grace window. `days=0` is atomic rotation;
            `days=null` (or omitted) clears the override and falls back to the plan default
            (api.DefaultAPIKeyGraceWindowDays = 7).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GraceWindowResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: SetGraceWindowRequest,
) -> GraceWindowResponse | Problem | None:
    """Set the per-account rotation grace-window override.

     Writes the override and invalidates the in-process cache so
    the next rotation observes the new value. `days=0` is atomic
    rotation (no grace); `days=null` (or omission) clears the
    override and falls back to the plan default.

    Args:
        body (SetGraceWindowRequest): Body of PATCH /v1/account/keys/grace_window_days. `days` is
            the per-account override for the rotation grace window. `days=0` is atomic rotation;
            `days=null` (or omitted) clears the override and falls back to the plan default
            (api.DefaultAPIKeyGraceWindowDays = 7).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GraceWindowResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
