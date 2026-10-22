import sys

import boto3
from botocore.config import Config

endpoint, access_key, secret_key = sys.argv[1:]
client = boto3.client(
    "s3", endpoint_url=endpoint, region_name="us-east-1",
    aws_access_key_id=access_key, aws_secret_access_key=secret_key,
    config=Config(
        s3={"addressing_style": "path"}, retries={"max_attempts": 0},
        request_checksum_calculation="when_supported",
        response_checksum_validation="when_supported",
    ),
)
try:
    common = {"Bucket": "assets", "Key": "integrity"}
    created = client.create_multipart_upload(
        **common, ChecksumAlgorithm="CRC32C", ChecksumType="FULL_OBJECT",
    )
    data = [b"a" * (5 * 1024 * 1024), b"last part bytes"]
    parts = []
    for number, body in enumerate(data, 1):
        part = client.upload_part(
            **common, UploadId=created["UploadId"], PartNumber=number,
            Body=body, ChecksumAlgorithm="CRC32C",
        )
        assert part.get("ChecksumCRC32C"), "part checksum missing"
        parts.append({
            "PartNumber": number, "ETag": part["ETag"],
            "ChecksumCRC32C": part["ChecksumCRC32C"],
        })
    complete = client.complete_multipart_upload(
        **common, UploadId=created["UploadId"], MultipartUpload={"Parts": parts},
    )
    assert complete.get("ChecksumCRC32C"), "completion checksum missing"
    read = client.get_object(**common, ChecksumMode="ENABLED")
    try:
        assert read["Body"].read() == b"".join(data), "download data changed"
    finally:
        read["Body"].close()
    head = client.head_object(**common, ChecksumMode="ENABLED")
    assert head["ChecksumCRC32C"] == complete["ChecksumCRC32C"], "HEAD checksum changed"
    attrs = client.get_object_attributes(
        **common, VersionId=complete["VersionId"],
        ObjectAttributes=["Checksum", "ObjectParts", "ObjectSize"], MaxParts=1,
    )
    assert attrs["Checksum"]["ChecksumCRC32C"] == complete["ChecksumCRC32C"]
    assert attrs["ObjectParts"]["IsTruncated"]
    assert len(attrs["ObjectParts"]["Parts"]) == 1
    assert attrs["VersionId"] == complete["VersionId"]
    print("Boto3 multipart integrity passed")
finally:
    client.close()
