from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/cors-presets/{id}".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Delete a CORS preset.

     Removes the cors_presets row. The FK ON DELETE SET
    NULL on edge_rules.cors_preset_id clears every
    referencing rule's FK atomically with the preset's
    deletion; the gatewayd-internal compile path fails
    closed (MergeCorsPresetIntoRule returns ErrNotFound)
    until the customer wires a new preset or inlines
    fallback values. The pgstore trigger fires
    pg_notify('cors_preset_changed', account_id) AFTER
    the DELETE commits.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Delete a CORS preset.

     Removes the cors_presets row. The FK ON DELETE SET
    NULL on edge_rules.cors_preset_id clears every
    referencing rule's FK atomically with the preset's
    deletion; the gatewayd-internal compile path fails
    closed (MergeCorsPresetIntoRule returns ErrNotFound)
    until the customer wires a new preset or inlines
    fallback values. The pgstore trigger fires
    pg_notify('cors_preset_changed', account_id) AFTER
    the DELETE commits.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Delete a CORS preset.

     Removes the cors_presets row. The FK ON DELETE SET
    NULL on edge_rules.cors_preset_id clears every
    referencing rule's FK atomically with the preset's
    deletion; the gatewayd-internal compile path fails
    closed (MergeCorsPresetIntoRule returns ErrNotFound)
    until the customer wires a new preset or inlines
    fallback values. The pgstore trigger fires
    pg_notify('cors_preset_changed', account_id) AFTER
    the DELETE commits.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Delete a CORS preset.

     Removes the cors_presets row. The FK ON DELETE SET
    NULL on edge_rules.cors_preset_id clears every
    referencing rule's FK atomically with the preset's
    deletion; the gatewayd-internal compile path fails
    closed (MergeCorsPresetIntoRule returns ErrNotFound)
    until the customer wires a new preset or inlines
    fallback values. The pgstore trigger fires
    pg_notify('cors_preset_changed', account_id) AFTER
    the DELETE commits.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
