from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.financial_budget_response import FinancialBudgetResponse
from ...models.problem import Problem
from ...models.update_financial_budget_request import UpdateFinancialBudgetRequest
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: UpdateFinancialBudgetRequest,
    idempotency_key: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/billing/budgets/{id}".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> FinancialBudgetResponse | Problem | None:
    if response.status_code == 200:
        response_200 = FinancialBudgetResponse.from_dict(response.json())

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[FinancialBudgetResponse | Problem]:
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
    body: UpdateFinancialBudgetRequest,
    idempotency_key: str,
) -> Response[FinancialBudgetResponse | Problem]:
    """Replace a budget draft at its expected revision.

     Requires admin scope, session MFA and Idempotency-Key. expected_revision
    must match the current policy; stale edits return 409. Set enabled=false:
    activation remains unavailable. Scope ownership and action eligibility are
    revalidated. Payment, security and user holds are independent of this intent.

    Args:
        id (UUID):
        idempotency_key (str):
        body (UpdateFinancialBudgetRequest): Complete replacement of budget intent guarded by its
            current revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FinancialBudgetResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateFinancialBudgetRequest,
    idempotency_key: str,
) -> FinancialBudgetResponse | Problem | None:
    """Replace a budget draft at its expected revision.

     Requires admin scope, session MFA and Idempotency-Key. expected_revision
    must match the current policy; stale edits return 409. Set enabled=false:
    activation remains unavailable. Scope ownership and action eligibility are
    revalidated. Payment, security and user holds are independent of this intent.

    Args:
        id (UUID):
        idempotency_key (str):
        body (UpdateFinancialBudgetRequest): Complete replacement of budget intent guarded by its
            current revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FinancialBudgetResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateFinancialBudgetRequest,
    idempotency_key: str,
) -> Response[FinancialBudgetResponse | Problem]:
    """Replace a budget draft at its expected revision.

     Requires admin scope, session MFA and Idempotency-Key. expected_revision
    must match the current policy; stale edits return 409. Set enabled=false:
    activation remains unavailable. Scope ownership and action eligibility are
    revalidated. Payment, security and user holds are independent of this intent.

    Args:
        id (UUID):
        idempotency_key (str):
        body (UpdateFinancialBudgetRequest): Complete replacement of budget intent guarded by its
            current revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FinancialBudgetResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateFinancialBudgetRequest,
    idempotency_key: str,
) -> FinancialBudgetResponse | Problem | None:
    """Replace a budget draft at its expected revision.

     Requires admin scope, session MFA and Idempotency-Key. expected_revision
    must match the current policy; stale edits return 409. Set enabled=false:
    activation remains unavailable. Scope ownership and action eligibility are
    revalidated. Payment, security and user holds are independent of this intent.

    Args:
        id (UUID):
        idempotency_key (str):
        body (UpdateFinancialBudgetRequest): Complete replacement of budget intent guarded by its
            current revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FinancialBudgetResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
