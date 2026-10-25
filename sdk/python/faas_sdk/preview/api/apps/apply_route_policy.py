from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_policy_apply_request import RoutePolicyApplyRequest
from ...models.route_policy_apply_response import RoutePolicyApplyResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: RoutePolicyApplyRequest,
    idempotency_key: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/route-policy/apply".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RoutePolicyApplyResponse | None:
    if response.status_code == 200:
        response_200 = RoutePolicyApplyResponse.from_dict(response.json())

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
) -> Response[Problem | RoutePolicyApplyResponse]:
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
    body: RoutePolicyApplyRequest,
    idempotency_key: str,
) -> Response[Problem | RoutePolicyApplyResponse]:
    """apply route policy.

     Recompute the reviewed fingerprint under account, app, and rule locks, plus deployment and captured-
    contract locks for group plans. Commit all changes and a durable receipt in one transaction.
    Requires deploy:write or admin and completed MFA. The same idempotency key and request recover the
    original receipt after a lost response. Gateway state is a separate observation; converging or
    unknown does not undo a committed receipt.

    Args:
        slug (str):
        idempotency_key (str):
        body (RoutePolicyApplyRequest): Supply exactly one source: inline requirements or
            saved=true. Saved apply requires expected_revision from the reviewed plan and rejects any
            saved intent revision change. The reviewed fingerprint binds its content hash, capture,
            options and configuration; patch bodies are recomputed under transaction locks. Identical
            committed retries return the original receipt even after saved intent changes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RoutePolicyApplyResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: RoutePolicyApplyRequest,
    idempotency_key: str,
) -> Problem | RoutePolicyApplyResponse | None:
    """apply route policy.

     Recompute the reviewed fingerprint under account, app, and rule locks, plus deployment and captured-
    contract locks for group plans. Commit all changes and a durable receipt in one transaction.
    Requires deploy:write or admin and completed MFA. The same idempotency key and request recover the
    original receipt after a lost response. Gateway state is a separate observation; converging or
    unknown does not undo a committed receipt.

    Args:
        slug (str):
        idempotency_key (str):
        body (RoutePolicyApplyRequest): Supply exactly one source: inline requirements or
            saved=true. Saved apply requires expected_revision from the reviewed plan and rejects any
            saved intent revision change. The reviewed fingerprint binds its content hash, capture,
            options and configuration; patch bodies are recomputed under transaction locks. Identical
            committed retries return the original receipt even after saved intent changes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RoutePolicyApplyResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: RoutePolicyApplyRequest,
    idempotency_key: str,
) -> Response[Problem | RoutePolicyApplyResponse]:
    """apply route policy.

     Recompute the reviewed fingerprint under account, app, and rule locks, plus deployment and captured-
    contract locks for group plans. Commit all changes and a durable receipt in one transaction.
    Requires deploy:write or admin and completed MFA. The same idempotency key and request recover the
    original receipt after a lost response. Gateway state is a separate observation; converging or
    unknown does not undo a committed receipt.

    Args:
        slug (str):
        idempotency_key (str):
        body (RoutePolicyApplyRequest): Supply exactly one source: inline requirements or
            saved=true. Saved apply requires expected_revision from the reviewed plan and rejects any
            saved intent revision change. The reviewed fingerprint binds its content hash, capture,
            options and configuration; patch bodies are recomputed under transaction locks. Identical
            committed retries return the original receipt even after saved intent changes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RoutePolicyApplyResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: RoutePolicyApplyRequest,
    idempotency_key: str,
) -> Problem | RoutePolicyApplyResponse | None:
    """apply route policy.

     Recompute the reviewed fingerprint under account, app, and rule locks, plus deployment and captured-
    contract locks for group plans. Commit all changes and a durable receipt in one transaction.
    Requires deploy:write or admin and completed MFA. The same idempotency key and request recover the
    original receipt after a lost response. Gateway state is a separate observation; converging or
    unknown does not undo a committed receipt.

    Args:
        slug (str):
        idempotency_key (str):
        body (RoutePolicyApplyRequest): Supply exactly one source: inline requirements or
            saved=true. Saved apply requires expected_revision from the reviewed plan and rejects any
            saved intent revision change. The reviewed fingerprint binds its content hash, capture,
            options and configuration; patch bodies are recomputed under transaction locks. Identical
            committed retries return the original receipt even after saved intent changes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RoutePolicyApplyResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
