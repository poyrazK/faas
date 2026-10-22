const [modules, endpoint, key, secret] = process.argv.slice(2);
const {
  S3Client, CreateMultipartUploadCommand, UploadPartCommand,
  CompleteMultipartUploadCommand, GetObjectCommand, HeadObjectCommand,
  GetObjectAttributesCommand,
} = require(`${modules}/@aws-sdk/client-s3`);

(async () => {
  const client = new S3Client({
    endpoint, region: 'us-east-1', forcePathStyle: true, maxAttempts: 1,
    credentials: { accessKeyId: key, secretAccessKey: secret },
    requestChecksumCalculation: 'WHEN_SUPPORTED',
    responseChecksumValidation: 'WHEN_SUPPORTED',
  });
  try {
    const common = { Bucket: 'assets', Key: 'integrity' };
    const created = await client.send(new CreateMultipartUploadCommand({
      ...common, ChecksumAlgorithm: 'CRC32C', ChecksumType: 'FULL_OBJECT',
    }));
    const data = [Buffer.alloc(5 * 1024 * 1024, 'a'), Buffer.from('last part bytes')];
    const parts = [];
    for (let i = 0; i < data.length; i++) {
      const part = await client.send(new UploadPartCommand({
        ...common, UploadId: created.UploadId, PartNumber: i + 1,
        Body: data[i], ContentLength: data[i].length, ChecksumAlgorithm: 'CRC32C',
      }));
      if (!part.ChecksumCRC32C) throw new Error('part checksum missing');
      parts.push({ PartNumber: i + 1, ETag: part.ETag, ChecksumCRC32C: part.ChecksumCRC32C });
    }
    const complete = await client.send(new CompleteMultipartUploadCommand({
      ...common, UploadId: created.UploadId, MultipartUpload: { Parts: parts },
    }));
    if (!complete.ChecksumCRC32C || complete.ChecksumType !== 'FULL_OBJECT') throw new Error('completion checksum missing');
    const read = await client.send(new GetObjectCommand({ ...common, ChecksumMode: 'ENABLED' }));
    const actual = Buffer.from(await read.Body.transformToByteArray());
    if (!actual.equals(Buffer.concat(data))) throw new Error('download data changed');
    const head = await client.send(new HeadObjectCommand({ ...common, ChecksumMode: 'ENABLED' }));
    if (head.ChecksumCRC32C !== complete.ChecksumCRC32C) throw new Error('HEAD checksum changed');
    const attrs = await client.send(new GetObjectAttributesCommand({
      ...common, VersionId: complete.VersionId,
      ObjectAttributes: ['Checksum', 'ObjectParts', 'ObjectSize'], MaxParts: 1,
    }));
    if (attrs.Checksum?.ChecksumCRC32C !== complete.ChecksumCRC32C ||
        attrs.ObjectParts?.Parts?.length !== 1 || !attrs.ObjectParts.IsTruncated ||
        attrs.VersionId !== complete.VersionId) throw new Error('attributes mismatch');
    console.log('Node AWS SDK multipart integrity passed');
  } finally {
    client.destroy();
  }
})().catch(error => { console.error(error.message); process.exitCode = 1; });
