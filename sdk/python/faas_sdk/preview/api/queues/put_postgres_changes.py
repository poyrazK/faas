from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.postgres_changes_response import PostgresChangesResponse
from ...models.problem import Problem
from ...models.put_postgres_changes_request import PutPostgresChangesRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: PutPostgresChangesRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/postgres-changes".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PostgresChangesResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PostgresChangesResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

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


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PostgresChangesResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutPostgresChangesRequest,
) -> Response[PostgresChangesResponse | Problem]:
    """Capture Postgres row changes into one of the app's queues.

     Declares which table columns are captured and the queue that receives each change. For a managed
    database Gregale installs capture and connects at once; for a database you run, finish with `gregale
    postgres-changes install`.

    Args:
        slug (str):
        body (PutPostgresChangesRequest): Declares the app's Postgres change capture.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PostgresChangesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutPostgresChangesRequest,
) -> PostgresChangesResponse | Problem | None:
    """Capture Postgres row changes into one of the app's queues.

     Declares which table columns are captured and the queue that receives each change. For a managed
    database Gregale installs capture and connects at once; for a database you run, finish with `gregale
    postgres-changes install`.

    Args:
        slug (str):
        body (PutPostgresChangesRequest): Declares the app's Postgres change capture.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PostgresChangesResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutPostgresChangesRequest,
) -> Response[PostgresChangesResponse | Problem]:
    """Capture Postgres row changes into one of the app's queues.

     Declares which table columns are captured and the queue that receives each change. For a managed
    database Gregale installs capture and connects at once; for a database you run, finish with `gregale
    postgres-changes install`.

    Args:
        slug (str):
        body (PutPostgresChangesRequest): Declares the app's Postgres change capture.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PostgresChangesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutPostgresChangesRequest,
) -> PostgresChangesResponse | Problem | None:
    """Capture Postgres row changes into one of the app's queues.

     Declares which table columns are captured and the queue that receives each change. For a managed
    database Gregale installs capture and connects at once; for a database you run, finish with `gregale
    postgres-changes install`.

    Args:
        slug (str):
        body (PutPostgresChangesRequest): Declares the app's Postgres change capture.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PostgresChangesResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
