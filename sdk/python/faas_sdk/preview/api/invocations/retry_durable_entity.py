from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.durable_entity_retry_request import DurableEntityRetryRequest
from ...models.durable_entity_retry_response import DurableEntityRetryResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: DurableEntityRetryRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/entities/retry".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DurableEntityRetryResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DurableEntityRetryResponse.from_dict(response.json())

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

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DurableEntityRetryResponse | Problem]:
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
    body: DurableEntityRetryRequest,
) -> Response[DurableEntityRetryResponse | Problem]:
    """Re-arm exactly one exhausted alarm or outgoing message.

     Account-owner mutation preview requiring deploy:write or admin, MFA where
    applicable, durable entity app enablement and the existing execution plan
    and active-customer rules. Account holds block recovery. Requires a fresh
    inspection's version and opaque recovery_revision plus alarm_at or head_id.
    The revision changes on every manifest write, including ownership and
    retry metadata changes. Only exhausted work on an unowned entity can be
    re-armed. A stale observation or non-exhausted target returns 409; an
    active owner returns 503. No missing entity is created.
    Recovery changes retry metadata only, preserving state, receipts, deadlines,
    message identities, payloads and durable transport acceptance. It does not
    invoke the guest, send a webhook or retry a terminal receiver delivery.
    Existing workers must be enabled separately. Responses are not cacheable.
    A repeated request after success returns 409. After an uncertain response,
    inspect again before deciding whether another recovery is needed.

    Args:
        slug (str):
        body (DurableEntityRetryRequest): For target alarm provide alarm_at and omit head_id; for
            outbox provide
            head_id and omit alarm_at. Copy comparison fields from fresh inspection.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DurableEntityRetryResponse | Problem]
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
    body: DurableEntityRetryRequest,
) -> DurableEntityRetryResponse | Problem | None:
    """Re-arm exactly one exhausted alarm or outgoing message.

     Account-owner mutation preview requiring deploy:write or admin, MFA where
    applicable, durable entity app enablement and the existing execution plan
    and active-customer rules. Account holds block recovery. Requires a fresh
    inspection's version and opaque recovery_revision plus alarm_at or head_id.
    The revision changes on every manifest write, including ownership and
    retry metadata changes. Only exhausted work on an unowned entity can be
    re-armed. A stale observation or non-exhausted target returns 409; an
    active owner returns 503. No missing entity is created.
    Recovery changes retry metadata only, preserving state, receipts, deadlines,
    message identities, payloads and durable transport acceptance. It does not
    invoke the guest, send a webhook or retry a terminal receiver delivery.
    Existing workers must be enabled separately. Responses are not cacheable.
    A repeated request after success returns 409. After an uncertain response,
    inspect again before deciding whether another recovery is needed.

    Args:
        slug (str):
        body (DurableEntityRetryRequest): For target alarm provide alarm_at and omit head_id; for
            outbox provide
            head_id and omit alarm_at. Copy comparison fields from fresh inspection.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DurableEntityRetryResponse | Problem
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
    body: DurableEntityRetryRequest,
) -> Response[DurableEntityRetryResponse | Problem]:
    """Re-arm exactly one exhausted alarm or outgoing message.

     Account-owner mutation preview requiring deploy:write or admin, MFA where
    applicable, durable entity app enablement and the existing execution plan
    and active-customer rules. Account holds block recovery. Requires a fresh
    inspection's version and opaque recovery_revision plus alarm_at or head_id.
    The revision changes on every manifest write, including ownership and
    retry metadata changes. Only exhausted work on an unowned entity can be
    re-armed. A stale observation or non-exhausted target returns 409; an
    active owner returns 503. No missing entity is created.
    Recovery changes retry metadata only, preserving state, receipts, deadlines,
    message identities, payloads and durable transport acceptance. It does not
    invoke the guest, send a webhook or retry a terminal receiver delivery.
    Existing workers must be enabled separately. Responses are not cacheable.
    A repeated request after success returns 409. After an uncertain response,
    inspect again before deciding whether another recovery is needed.

    Args:
        slug (str):
        body (DurableEntityRetryRequest): For target alarm provide alarm_at and omit head_id; for
            outbox provide
            head_id and omit alarm_at. Copy comparison fields from fresh inspection.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DurableEntityRetryResponse | Problem]
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
    body: DurableEntityRetryRequest,
) -> DurableEntityRetryResponse | Problem | None:
    """Re-arm exactly one exhausted alarm or outgoing message.

     Account-owner mutation preview requiring deploy:write or admin, MFA where
    applicable, durable entity app enablement and the existing execution plan
    and active-customer rules. Account holds block recovery. Requires a fresh
    inspection's version and opaque recovery_revision plus alarm_at or head_id.
    The revision changes on every manifest write, including ownership and
    retry metadata changes. Only exhausted work on an unowned entity can be
    re-armed. A stale observation or non-exhausted target returns 409; an
    active owner returns 503. No missing entity is created.
    Recovery changes retry metadata only, preserving state, receipts, deadlines,
    message identities, payloads and durable transport acceptance. It does not
    invoke the guest, send a webhook or retry a terminal receiver delivery.
    Existing workers must be enabled separately. Responses are not cacheable.
    A repeated request after success returns 409. After an uncertain response,
    inspect again before deciding whether another recovery is needed.

    Args:
        slug (str):
        body (DurableEntityRetryRequest): For target alarm provide alarm_at and omit head_id; for
            outbox provide
            head_id and omit alarm_at. Copy comparison fields from fresh inspection.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DurableEntityRetryResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
