from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.run_binding_kind import RunBindingKind
from ...types import UNSET, Response


def _get_kwargs(
    kind: RunBindingKind,
    id: UUID,
    *,
    expected_revision: int,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["expected_revision"] = expected_revision

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/run-controls/bindings/{kind}/{id}".format(
            kind=quote(str(kind), safe=""),
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

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
    kind: RunBindingKind,
    id: UUID,
    *,
    client: AuthenticatedClient,
    expected_revision: int,
) -> Response[Any | Problem]:
    """Disable a binding while retaining its revision tombstone.

    Args:
        kind (RunBindingKind): Credential or Agent endpoint type used for Run attribution.
        id (UUID):
        expected_revision (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        kind=kind,
        id=id,
        expected_revision=expected_revision,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    kind: RunBindingKind,
    id: UUID,
    *,
    client: AuthenticatedClient,
    expected_revision: int,
) -> Any | Problem | None:
    """Disable a binding while retaining its revision tombstone.

    Args:
        kind (RunBindingKind): Credential or Agent endpoint type used for Run attribution.
        id (UUID):
        expected_revision (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        kind=kind,
        id=id,
        client=client,
        expected_revision=expected_revision,
    ).parsed


async def asyncio_detailed(
    kind: RunBindingKind,
    id: UUID,
    *,
    client: AuthenticatedClient,
    expected_revision: int,
) -> Response[Any | Problem]:
    """Disable a binding while retaining its revision tombstone.

    Args:
        kind (RunBindingKind): Credential or Agent endpoint type used for Run attribution.
        id (UUID):
        expected_revision (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        kind=kind,
        id=id,
        expected_revision=expected_revision,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    kind: RunBindingKind,
    id: UUID,
    *,
    client: AuthenticatedClient,
    expected_revision: int,
) -> Any | Problem | None:
    """Disable a binding while retaining its revision tombstone.

    Args:
        kind (RunBindingKind): Credential or Agent endpoint type used for Run attribution.
        id (UUID):
        expected_revision (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            kind=kind,
            id=id,
            client=client,
            expected_revision=expected_revision,
        )
    ).parsed
