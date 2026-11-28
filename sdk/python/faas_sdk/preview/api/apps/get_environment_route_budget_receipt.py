from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.environment_route_budget_receipt import EnvironmentRouteBudgetReceipt
from ...models.get_environment_route_budget_receipt_scope import (
    GetEnvironmentRouteBudgetReceiptScope,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    request_id: UUID,
    *,
    scope: GetEnvironmentRouteBudgetReceiptScope | Unset = UNSET,
    environment: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_scope: str | Unset = UNSET
    if not isinstance(scope, Unset):
        json_scope = scope

    params["scope"] = json_scope

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/route-budgets/receipts/{request_id}".format(
            slug=quote(str(slug), safe=""),
            request_id=quote(str(request_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EnvironmentRouteBudgetReceipt | Problem | None:
    if response.status_code == 200:
        response_200 = EnvironmentRouteBudgetReceipt.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EnvironmentRouteBudgetReceipt | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    request_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    scope: GetEnvironmentRouteBudgetReceiptScope | Unset = UNSET,
    environment: str | Unset = UNSET,
) -> Response[EnvironmentRouteBudgetReceipt | Problem]:
    """Read an original durable environment budget receipt.

     Requires current app read access and completed MFA. Matches account, app and current immutable
    project/environment identities. Returns historical committed policy revisions and the exact original
    request fingerprint, excluding configuration values. Later policy edits do not replace this receipt.
    A missing, inaccessible or unavailable receipt never proves non-commit. This endpoint performs no
    mutation, replay, approval or expiry renewal and does not establish runtime health.

    Args:
        slug (str):
        request_id (UUID):
        scope (GetEnvironmentRouteBudgetReceiptScope | Unset):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentRouteBudgetReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        request_id=request_id,
        scope=scope,
        environment=environment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    request_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    scope: GetEnvironmentRouteBudgetReceiptScope | Unset = UNSET,
    environment: str | Unset = UNSET,
) -> EnvironmentRouteBudgetReceipt | Problem | None:
    """Read an original durable environment budget receipt.

     Requires current app read access and completed MFA. Matches account, app and current immutable
    project/environment identities. Returns historical committed policy revisions and the exact original
    request fingerprint, excluding configuration values. Later policy edits do not replace this receipt.
    A missing, inaccessible or unavailable receipt never proves non-commit. This endpoint performs no
    mutation, replay, approval or expiry renewal and does not establish runtime health.

    Args:
        slug (str):
        request_id (UUID):
        scope (GetEnvironmentRouteBudgetReceiptScope | Unset):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentRouteBudgetReceipt | Problem
    """

    return sync_detailed(
        slug=slug,
        request_id=request_id,
        client=client,
        scope=scope,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    request_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    scope: GetEnvironmentRouteBudgetReceiptScope | Unset = UNSET,
    environment: str | Unset = UNSET,
) -> Response[EnvironmentRouteBudgetReceipt | Problem]:
    """Read an original durable environment budget receipt.

     Requires current app read access and completed MFA. Matches account, app and current immutable
    project/environment identities. Returns historical committed policy revisions and the exact original
    request fingerprint, excluding configuration values. Later policy edits do not replace this receipt.
    A missing, inaccessible or unavailable receipt never proves non-commit. This endpoint performs no
    mutation, replay, approval or expiry renewal and does not establish runtime health.

    Args:
        slug (str):
        request_id (UUID):
        scope (GetEnvironmentRouteBudgetReceiptScope | Unset):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentRouteBudgetReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        request_id=request_id,
        scope=scope,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    request_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    scope: GetEnvironmentRouteBudgetReceiptScope | Unset = UNSET,
    environment: str | Unset = UNSET,
) -> EnvironmentRouteBudgetReceipt | Problem | None:
    """Read an original durable environment budget receipt.

     Requires current app read access and completed MFA. Matches account, app and current immutable
    project/environment identities. Returns historical committed policy revisions and the exact original
    request fingerprint, excluding configuration values. Later policy edits do not replace this receipt.
    A missing, inaccessible or unavailable receipt never proves non-commit. This endpoint performs no
    mutation, replay, approval or expiry renewal and does not establish runtime health.

    Args:
        slug (str):
        request_id (UUID):
        scope (GetEnvironmentRouteBudgetReceiptScope | Unset):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentRouteBudgetReceipt | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            request_id=request_id,
            client=client,
            scope=scope,
            environment=environment,
        )
    ).parsed
