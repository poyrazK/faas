from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.financial_budget_history_response import FinancialBudgetHistoryResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    *,
    after_revision: int | Unset = 0,
    limit: int | Unset = 100,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["after_revision"] = after_revision

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/billing/budgets/{id}/revisions".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> FinancialBudgetHistoryResponse | Problem | None:
    if response.status_code == 200:
        response_200 = FinancialBudgetHistoryResponse.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[FinancialBudgetHistoryResponse | Problem]:
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
    after_revision: int | Unset = 0,
    limit: int | Unset = 100,
) -> Response[FinancialBudgetHistoryResponse | Problem]:
    """Page through immutable budget revisions, including deleted policies.

     Requires usage:read and session MFA. Ownership is checked before history is read. Use next_revision
    as after_revision for continuation; a final full page may be followed by an empty page.

    Args:
        id (UUID):
        after_revision (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FinancialBudgetHistoryResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        after_revision=after_revision,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    after_revision: int | Unset = 0,
    limit: int | Unset = 100,
) -> FinancialBudgetHistoryResponse | Problem | None:
    """Page through immutable budget revisions, including deleted policies.

     Requires usage:read and session MFA. Ownership is checked before history is read. Use next_revision
    as after_revision for continuation; a final full page may be followed by an empty page.

    Args:
        id (UUID):
        after_revision (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FinancialBudgetHistoryResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        after_revision=after_revision,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    after_revision: int | Unset = 0,
    limit: int | Unset = 100,
) -> Response[FinancialBudgetHistoryResponse | Problem]:
    """Page through immutable budget revisions, including deleted policies.

     Requires usage:read and session MFA. Ownership is checked before history is read. Use next_revision
    as after_revision for continuation; a final full page may be followed by an empty page.

    Args:
        id (UUID):
        after_revision (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FinancialBudgetHistoryResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        after_revision=after_revision,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    after_revision: int | Unset = 0,
    limit: int | Unset = 100,
) -> FinancialBudgetHistoryResponse | Problem | None:
    """Page through immutable budget revisions, including deleted policies.

     Requires usage:read and session MFA. Ownership is checked before history is read. Use next_revision
    as after_revision for continuation; a final full page may be followed by an empty page.

    Args:
        id (UUID):
        after_revision (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FinancialBudgetHistoryResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            after_revision=after_revision,
            limit=limit,
        )
    ).parsed
