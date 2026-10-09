from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_service_map_range import GetServiceMapRange
from ...models.problem import Problem
from ...models.service_map_response import ServiceMapResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    range_: GetServiceMapRange | Unset = "1h",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_range_: str | Unset = UNSET
    if not isinstance(range_, Unset):
        json_range_ = range_

    params["range"] = json_range_

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/service-map",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ServiceMapResponse | None:
    if response.status_code == 200:
        response_200 = ServiceMapResponse.from_dict(response.json())

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
) -> Response[Problem | ServiceMapResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    range_: GetServiceMapRange | Unset = "1h",
) -> Response[Problem | ServiceMapResponse]:
    r"""Account service map of caller → target app edges.

     ADR-740 internal preview, enabled by `FAAS_SERVICE_MAP_ENABLED=1`;
    otherwise 503 `service_map_unavailable`.

    Built from the unsampled service-proxy edge series. Both the
    caller and the target are constrained to the account's apps.
    Edges are ranked by call volume and capped at 500
    (`truncated: true`). Latency percentiles cover successful calls
    only. Prometheus failure returns 200 with null `nodes`/`edges`
    and `source: \"degraded: <reason>\"`, as `/v1/apps/metrics` does.

    Args:
        range_ (GetServiceMapRange | Unset):  Default: '1h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ServiceMapResponse]
    """

    kwargs = _get_kwargs(
        range_=range_,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    range_: GetServiceMapRange | Unset = "1h",
) -> Problem | ServiceMapResponse | None:
    r"""Account service map of caller → target app edges.

     ADR-740 internal preview, enabled by `FAAS_SERVICE_MAP_ENABLED=1`;
    otherwise 503 `service_map_unavailable`.

    Built from the unsampled service-proxy edge series. Both the
    caller and the target are constrained to the account's apps.
    Edges are ranked by call volume and capped at 500
    (`truncated: true`). Latency percentiles cover successful calls
    only. Prometheus failure returns 200 with null `nodes`/`edges`
    and `source: \"degraded: <reason>\"`, as `/v1/apps/metrics` does.

    Args:
        range_ (GetServiceMapRange | Unset):  Default: '1h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ServiceMapResponse
    """

    return sync_detailed(
        client=client,
        range_=range_,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    range_: GetServiceMapRange | Unset = "1h",
) -> Response[Problem | ServiceMapResponse]:
    r"""Account service map of caller → target app edges.

     ADR-740 internal preview, enabled by `FAAS_SERVICE_MAP_ENABLED=1`;
    otherwise 503 `service_map_unavailable`.

    Built from the unsampled service-proxy edge series. Both the
    caller and the target are constrained to the account's apps.
    Edges are ranked by call volume and capped at 500
    (`truncated: true`). Latency percentiles cover successful calls
    only. Prometheus failure returns 200 with null `nodes`/`edges`
    and `source: \"degraded: <reason>\"`, as `/v1/apps/metrics` does.

    Args:
        range_ (GetServiceMapRange | Unset):  Default: '1h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ServiceMapResponse]
    """

    kwargs = _get_kwargs(
        range_=range_,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    range_: GetServiceMapRange | Unset = "1h",
) -> Problem | ServiceMapResponse | None:
    r"""Account service map of caller → target app edges.

     ADR-740 internal preview, enabled by `FAAS_SERVICE_MAP_ENABLED=1`;
    otherwise 503 `service_map_unavailable`.

    Built from the unsampled service-proxy edge series. Both the
    caller and the target are constrained to the account's apps.
    Edges are ranked by call volume and capped at 500
    (`truncated: true`). Latency percentiles cover successful calls
    only. Prometheus failure returns 200 with null `nodes`/`edges`
    and `source: \"degraded: <reason>\"`, as `/v1/apps/metrics` does.

    Args:
        range_ (GetServiceMapRange | Unset):  Default: '1h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ServiceMapResponse
    """

    return (
        await asyncio_detailed(
            client=client,
            range_=range_,
        )
    ).parsed
