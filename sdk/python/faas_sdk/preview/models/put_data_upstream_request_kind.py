from typing import Literal

PutDataUpstreamRequestKind = Literal[
    "cassandra",
    "clickhouse",
    "elasticsearch",
    "etcd",
    "https_api",
    "kafka",
    "memcached",
    "minio",
    "mongo",
    "nats",
    "opensearch",
    "postgres",
    "rabbitmq",
    "redis",
    "s3",
]

PUT_DATA_UPSTREAM_REQUEST_KIND_VALUES: set[PutDataUpstreamRequestKind] = {
    "cassandra",
    "clickhouse",
    "elasticsearch",
    "etcd",
    "https_api",
    "kafka",
    "memcached",
    "minio",
    "mongo",
    "nats",
    "opensearch",
    "postgres",
    "rabbitmq",
    "redis",
    "s3",
}


def check_put_data_upstream_request_kind(value: str) -> PutDataUpstreamRequestKind:
    if value in PUT_DATA_UPSTREAM_REQUEST_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUT_DATA_UPSTREAM_REQUEST_KIND_VALUES!r}")
