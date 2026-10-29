from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_submission_lookup_request import OperationSubmissionLookupRequest
from ...models.operation_submission_lookup_response import OperationSubmissionLookupResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: OperationSubmissionLookupRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/platform-tenant-self/customer-operations/submissions/lookup",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationSubmissionLookupResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationSubmissionLookupResponse.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

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
) -> Response[OperationSubmissionLookupResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: OperationSubmissionLookupRequest,
) -> Response[OperationSubmissionLookupResponse | Problem]:
    """Resolve a customer's retained submission without admitting work.

     Requires platform_tenant:operations:read. Reads the idempotency ledger scoped to the authenticated
    account/customer, app, environment, operation name and key. Available while admission is closed,
    independently of current code or definition availability. accepted includes the original acceptance
    time and receipt; fetch status for current business state. expired means a known identity no longer
    has a retained usable acceptance. unresolved is not proof of rejection: admission may be in flight
    or a receipt pruned. No work is submitted and Cache-Control is no-store. expected_identity is an
    optional principal fence, never an ownership grant. Selectors belong in the bounded JSON body, not
    query parameters.

    Args:
        body (OperationSubmissionLookupRequest): Selectors for an authenticated customer's
            retained idempotency identity; bounded to 4096 bytes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationSubmissionLookupResponse | Problem]
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
    body: OperationSubmissionLookupRequest,
) -> OperationSubmissionLookupResponse | Problem | None:
    """Resolve a customer's retained submission without admitting work.

     Requires platform_tenant:operations:read. Reads the idempotency ledger scoped to the authenticated
    account/customer, app, environment, operation name and key. Available while admission is closed,
    independently of current code or definition availability. accepted includes the original acceptance
    time and receipt; fetch status for current business state. expired means a known identity no longer
    has a retained usable acceptance. unresolved is not proof of rejection: admission may be in flight
    or a receipt pruned. No work is submitted and Cache-Control is no-store. expected_identity is an
    optional principal fence, never an ownership grant. Selectors belong in the bounded JSON body, not
    query parameters.

    Args:
        body (OperationSubmissionLookupRequest): Selectors for an authenticated customer's
            retained idempotency identity; bounded to 4096 bytes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationSubmissionLookupResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: OperationSubmissionLookupRequest,
) -> Response[OperationSubmissionLookupResponse | Problem]:
    """Resolve a customer's retained submission without admitting work.

     Requires platform_tenant:operations:read. Reads the idempotency ledger scoped to the authenticated
    account/customer, app, environment, operation name and key. Available while admission is closed,
    independently of current code or definition availability. accepted includes the original acceptance
    time and receipt; fetch status for current business state. expired means a known identity no longer
    has a retained usable acceptance. unresolved is not proof of rejection: admission may be in flight
    or a receipt pruned. No work is submitted and Cache-Control is no-store. expected_identity is an
    optional principal fence, never an ownership grant. Selectors belong in the bounded JSON body, not
    query parameters.

    Args:
        body (OperationSubmissionLookupRequest): Selectors for an authenticated customer's
            retained idempotency identity; bounded to 4096 bytes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationSubmissionLookupResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: OperationSubmissionLookupRequest,
) -> OperationSubmissionLookupResponse | Problem | None:
    """Resolve a customer's retained submission without admitting work.

     Requires platform_tenant:operations:read. Reads the idempotency ledger scoped to the authenticated
    account/customer, app, environment, operation name and key. Available while admission is closed,
    independently of current code or definition availability. accepted includes the original acceptance
    time and receipt; fetch status for current business state. expired means a known identity no longer
    has a retained usable acceptance. unresolved is not proof of rejection: admission may be in flight
    or a receipt pruned. No work is submitted and Cache-Control is no-store. expected_identity is an
    optional principal fence, never an ownership grant. Selectors belong in the bounded JSON body, not
    query parameters.

    Args:
        body (OperationSubmissionLookupRequest): Selectors for an authenticated customer's
            retained idempotency identity; bounded to 4096 bytes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationSubmissionLookupResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
