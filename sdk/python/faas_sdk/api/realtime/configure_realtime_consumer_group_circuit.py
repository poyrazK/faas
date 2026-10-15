from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.realtime_consumer_group_circuit_policy import RealtimeConsumerGroupCircuitPolicy
from ...models.realtime_consumer_group_circuit_status import RealtimeConsumerGroupCircuitStatus
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    body: RealtimeConsumerGroupCircuitPolicy,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/circuit".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
            group=quote(str(group), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | RealtimeConsumerGroupCircuitStatus | None:
    if response.status_code == 200:
        response_200 = RealtimeConsumerGroupCircuitStatus.from_dict(response.json())

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
) -> Response[Any | RealtimeConsumerGroupCircuitStatus]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeConsumerGroupCircuitPolicy,
) -> Response[Any | RealtimeConsumerGroupCircuitStatus]:
    """Configure consumer group circuit breaker

     Configure consumer group circuit breaker. Opt-in consecutive processing-failure breaker; default
    disabled, threshold 5 and cooldown 30 seconds. Reported retries/dead failures and lease expiries
    count; normal delivery-deadline expiry does not. Manual pause/drain retain precedence. Cooldown
    transitions on claims to one fenced probe at a time; only its success restores admission. Existing
    leases may settle. Identical policy writes preserve state; changed policy resets closed. Status is
    observational; reads do not advance cooldown. Account ownership and MFA required; reads need read
    scope, writes admin scope. Scoped worker claims already obey the gate with no new grant.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupCircuitPolicy): Realtime Consumer Group Circuit Policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | RealtimeConsumerGroupCircuitStatus]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeConsumerGroupCircuitPolicy,
) -> Any | RealtimeConsumerGroupCircuitStatus | None:
    """Configure consumer group circuit breaker

     Configure consumer group circuit breaker. Opt-in consecutive processing-failure breaker; default
    disabled, threshold 5 and cooldown 30 seconds. Reported retries/dead failures and lease expiries
    count; normal delivery-deadline expiry does not. Manual pause/drain retain precedence. Cooldown
    transitions on claims to one fenced probe at a time; only its success restores admission. Existing
    leases may settle. Identical policy writes preserve state; changed policy resets closed. Status is
    observational; reads do not advance cooldown. Account ownership and MFA required; reads need read
    scope, writes admin scope. Scoped worker claims already obey the gate with no new grant.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupCircuitPolicy): Realtime Consumer Group Circuit Policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | RealtimeConsumerGroupCircuitStatus
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeConsumerGroupCircuitPolicy,
) -> Response[Any | RealtimeConsumerGroupCircuitStatus]:
    """Configure consumer group circuit breaker

     Configure consumer group circuit breaker. Opt-in consecutive processing-failure breaker; default
    disabled, threshold 5 and cooldown 30 seconds. Reported retries/dead failures and lease expiries
    count; normal delivery-deadline expiry does not. Manual pause/drain retain precedence. Cooldown
    transitions on claims to one fenced probe at a time; only its success restores admission. Existing
    leases may settle. Identical policy writes preserve state; changed policy resets closed. Status is
    observational; reads do not advance cooldown. Account ownership and MFA required; reads need read
    scope, writes admin scope. Scoped worker claims already obey the gate with no new grant.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupCircuitPolicy): Realtime Consumer Group Circuit Policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | RealtimeConsumerGroupCircuitStatus]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeConsumerGroupCircuitPolicy,
) -> Any | RealtimeConsumerGroupCircuitStatus | None:
    """Configure consumer group circuit breaker

     Configure consumer group circuit breaker. Opt-in consecutive processing-failure breaker; default
    disabled, threshold 5 and cooldown 30 seconds. Reported retries/dead failures and lease expiries
    count; normal delivery-deadline expiry does not. Manual pause/drain retain precedence. Cooldown
    transitions on claims to one fenced probe at a time; only its success restores admission. Existing
    leases may settle. Identical policy writes preserve state; changed policy resets closed. Status is
    observational; reads do not advance cooldown. Account ownership and MFA required; reads need read
    scope, writes admin scope. Scoped worker claims already obey the gate with no new grant.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupCircuitPolicy): Realtime Consumer Group Circuit Policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | RealtimeConsumerGroupCircuitStatus
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            group=group,
            client=client,
            body=body,
        )
    ).parsed
