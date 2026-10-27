from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_mcp_readiness_pager_duty_acknowledgements_destination import (
    GetMCPReadinessPagerDutyAcknowledgementsDestination,
)
from ...models.mcp_readiness_pager_duty_acknowledgement_policy import MCPReadinessPagerDutyAcknowledgementPolicy
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    destination: GetMCPReadinessPagerDutyAcknowledgementsDestination | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_destination: str | Unset = UNSET
    if not isinstance(destination, Unset):
        json_destination = destination

    params["destination"] = json_destination

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/alerts/{id}/mcp-readiness-pagerduty-acknowledgements".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | MCPReadinessPagerDutyAcknowledgementPolicy | None:
    if response.status_code == 200:
        response_200 = MCPReadinessPagerDutyAcknowledgementPolicy.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | MCPReadinessPagerDutyAcknowledgementPolicy]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    destination: GetMCPReadinessPagerDutyAcknowledgementsDestination | Unset = UNSET,
) -> Response[Any | MCPReadinessPagerDutyAcknowledgementPolicy]:
    """Read masked PagerDuty webhook acknowledgement settings

     Read scope and MFA required. Version zero is disabled. The stable relative webhook path is returned
    before configuration; concatenate it with the trusted API origin when creating the PagerDuty v3
    subscription.

    Args:
        slug (str):
        id (UUID):
        destination (GetMCPReadinessPagerDutyAcknowledgementsDestination | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessPagerDutyAcknowledgementPolicy]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        destination=destination,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    destination: GetMCPReadinessPagerDutyAcknowledgementsDestination | Unset = UNSET,
) -> Any | MCPReadinessPagerDutyAcknowledgementPolicy | None:
    """Read masked PagerDuty webhook acknowledgement settings

     Read scope and MFA required. Version zero is disabled. The stable relative webhook path is returned
    before configuration; concatenate it with the trusted API origin when creating the PagerDuty v3
    subscription.

    Args:
        slug (str):
        id (UUID):
        destination (GetMCPReadinessPagerDutyAcknowledgementsDestination | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessPagerDutyAcknowledgementPolicy
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        destination=destination,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    destination: GetMCPReadinessPagerDutyAcknowledgementsDestination | Unset = UNSET,
) -> Response[Any | MCPReadinessPagerDutyAcknowledgementPolicy]:
    """Read masked PagerDuty webhook acknowledgement settings

     Read scope and MFA required. Version zero is disabled. The stable relative webhook path is returned
    before configuration; concatenate it with the trusted API origin when creating the PagerDuty v3
    subscription.

    Args:
        slug (str):
        id (UUID):
        destination (GetMCPReadinessPagerDutyAcknowledgementsDestination | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessPagerDutyAcknowledgementPolicy]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        destination=destination,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    destination: GetMCPReadinessPagerDutyAcknowledgementsDestination | Unset = UNSET,
) -> Any | MCPReadinessPagerDutyAcknowledgementPolicy | None:
    """Read masked PagerDuty webhook acknowledgement settings

     Read scope and MFA required. Version zero is disabled. The stable relative webhook path is returned
    before configuration; concatenate it with the trusted API origin when creating the PagerDuty v3
    subscription.

    Args:
        slug (str):
        id (UUID):
        destination (GetMCPReadinessPagerDutyAcknowledgementsDestination | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessPagerDutyAcknowledgementPolicy
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            destination=destination,
        )
    ).parsed
