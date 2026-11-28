from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.cors_preset_list_response import CorsPresetListResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    app_id: UUID | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_app_id: str | Unset = UNSET
    if not isinstance(app_id, Unset):
        json_app_id = str(app_id)
    params["app_id"] = json_app_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/cors-presets",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CorsPresetListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CorsPresetListResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[CorsPresetListResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID | Unset = UNSET,
) -> Response[CorsPresetListResponse | Problem]:
    """List CORS presets visible to the calling account.

     Lists every cors_presets row the account owns — both
    account-wide (app_id IS NULL) and app-scoped (app_id
    is set). The optional `app_id` query parameter filters
    to a single app's scoped presets; absent = union of
    account-wide + every app-scoped row. No pagination —
    the per-account quota caps the row count (see the
    plan_cors_preset_quota_reached error code).

    Args:
        app_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CorsPresetListResponse | Problem]
    """

    kwargs = _get_kwargs(
        app_id=app_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID | Unset = UNSET,
) -> CorsPresetListResponse | Problem | None:
    """List CORS presets visible to the calling account.

     Lists every cors_presets row the account owns — both
    account-wide (app_id IS NULL) and app-scoped (app_id
    is set). The optional `app_id` query parameter filters
    to a single app's scoped presets; absent = union of
    account-wide + every app-scoped row. No pagination —
    the per-account quota caps the row count (see the
    plan_cors_preset_quota_reached error code).

    Args:
        app_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CorsPresetListResponse | Problem
    """

    return sync_detailed(
        client=client,
        app_id=app_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID | Unset = UNSET,
) -> Response[CorsPresetListResponse | Problem]:
    """List CORS presets visible to the calling account.

     Lists every cors_presets row the account owns — both
    account-wide (app_id IS NULL) and app-scoped (app_id
    is set). The optional `app_id` query parameter filters
    to a single app's scoped presets; absent = union of
    account-wide + every app-scoped row. No pagination —
    the per-account quota caps the row count (see the
    plan_cors_preset_quota_reached error code).

    Args:
        app_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CorsPresetListResponse | Problem]
    """

    kwargs = _get_kwargs(
        app_id=app_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID | Unset = UNSET,
) -> CorsPresetListResponse | Problem | None:
    """List CORS presets visible to the calling account.

     Lists every cors_presets row the account owns — both
    account-wide (app_id IS NULL) and app-scoped (app_id
    is set). The optional `app_id` query parameter filters
    to a single app's scoped presets; absent = union of
    account-wide + every app-scoped row. No pagination —
    the per-account quota caps the row count (see the
    plan_cors_preset_quota_reached error code).

    Args:
        app_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CorsPresetListResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            app_id=app_id,
        )
    ).parsed
