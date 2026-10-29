from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.realtime_consumer_group_settlement import RealtimeConsumerGroupSettlement
from ...models.realtime_consumer_group_settlement_result import RealtimeConsumerGroupSettlementResult
from ...types import Response


def _get_kwargs(
    group_id: str,
    *,
    body: RealtimeConsumerGroupSettlement,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/realtime/workers/groups/{group_id}/claims/settle".format(
            group_id=quote(str(group_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RealtimeConsumerGroupSettlementResult | None:
    if response.status_code == 200:
        response_200 = RealtimeConsumerGroupSettlementResult.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RealtimeConsumerGroupSettlementResult]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    group_id: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeConsumerGroupSettlement,
) -> Response[Problem | RealtimeConsumerGroupSettlementResult]:
    """Settle or renew only this credential’s own lease

     Requires the exact action permission: complete, retry, renew or release. The credential can settle
    only leases issued to its stable credential ID. Rotation preserves that ID and claim ownership. New
    settlements require unexpired claim tokens. Matching receipts return the original result after
    current authorization, even when the original lease has ended. Expired/revoked credentials cannot
    settle or replay work.

    Args:
        group_id (str):
        body (RealtimeConsumerGroupSettlement): Realtime Consumer Group Settlement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RealtimeConsumerGroupSettlementResult]
    """

    kwargs = _get_kwargs(
        group_id=group_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    group_id: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeConsumerGroupSettlement,
) -> Problem | RealtimeConsumerGroupSettlementResult | None:
    """Settle or renew only this credential’s own lease

     Requires the exact action permission: complete, retry, renew or release. The credential can settle
    only leases issued to its stable credential ID. Rotation preserves that ID and claim ownership. New
    settlements require unexpired claim tokens. Matching receipts return the original result after
    current authorization, even when the original lease has ended. Expired/revoked credentials cannot
    settle or replay work.

    Args:
        group_id (str):
        body (RealtimeConsumerGroupSettlement): Realtime Consumer Group Settlement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RealtimeConsumerGroupSettlementResult
    """

    return sync_detailed(
        group_id=group_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    group_id: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeConsumerGroupSettlement,
) -> Response[Problem | RealtimeConsumerGroupSettlementResult]:
    """Settle or renew only this credential’s own lease

     Requires the exact action permission: complete, retry, renew or release. The credential can settle
    only leases issued to its stable credential ID. Rotation preserves that ID and claim ownership. New
    settlements require unexpired claim tokens. Matching receipts return the original result after
    current authorization, even when the original lease has ended. Expired/revoked credentials cannot
    settle or replay work.

    Args:
        group_id (str):
        body (RealtimeConsumerGroupSettlement): Realtime Consumer Group Settlement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RealtimeConsumerGroupSettlementResult]
    """

    kwargs = _get_kwargs(
        group_id=group_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    group_id: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeConsumerGroupSettlement,
) -> Problem | RealtimeConsumerGroupSettlementResult | None:
    """Settle or renew only this credential’s own lease

     Requires the exact action permission: complete, retry, renew or release. The credential can settle
    only leases issued to its stable credential ID. Rotation preserves that ID and claim ownership. New
    settlements require unexpired claim tokens. Matching receipts return the original result after
    current authorization, even when the original lease has ended. Expired/revoked credentials cannot
    settle or replay work.

    Args:
        group_id (str):
        body (RealtimeConsumerGroupSettlement): Realtime Consumer Group Settlement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RealtimeConsumerGroupSettlementResult
    """

    return (
        await asyncio_detailed(
            group_id=group_id,
            client=client,
            body=body,
        )
    ).parsed
