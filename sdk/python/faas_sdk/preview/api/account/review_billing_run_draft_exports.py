from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.billing_run_draft_export_review import BillingRunDraftExportReview
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    *,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/billing-runs/{id}/draft-exports".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> BillingRunDraftExportReview | Problem | None:
    if response.status_code == 200:
        response_200 = BillingRunDraftExportReview.from_dict(response.json())

        return response_200

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[BillingRunDraftExportReview | Problem]:
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
    cursor: str | Unset = UNSET,
) -> Response[BillingRunDraftExportReview | Problem]:
    """Review Stripe draft export eligibility for one page of a completed run.

     Reads the original run items and checks the current statements and connection without calling
    Stripe.

    Args:
        id (UUID):
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingRunDraftExportReview | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        cursor=cursor,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    cursor: str | Unset = UNSET,
) -> BillingRunDraftExportReview | Problem | None:
    """Review Stripe draft export eligibility for one page of a completed run.

     Reads the original run items and checks the current statements and connection without calling
    Stripe.

    Args:
        id (UUID):
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingRunDraftExportReview | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    cursor: str | Unset = UNSET,
) -> Response[BillingRunDraftExportReview | Problem]:
    """Review Stripe draft export eligibility for one page of a completed run.

     Reads the original run items and checks the current statements and connection without calling
    Stripe.

    Args:
        id (UUID):
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingRunDraftExportReview | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        cursor=cursor,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    cursor: str | Unset = UNSET,
) -> BillingRunDraftExportReview | Problem | None:
    """Review Stripe draft export eligibility for one page of a completed run.

     Reads the original run items and checks the current statements and connection without calling
    Stripe.

    Args:
        id (UUID):
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingRunDraftExportReview | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            cursor=cursor,
        )
    ).parsed
