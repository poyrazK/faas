import test from 'node:test'
import assert from 'node:assert/strict'
import { objectResource, objectURL, presign, storageBindings } from '../storage.mjs'

test('matches the AWS SigV4 presigned URL example', () => {
  // AWS Signature Version 4 documentation, "Authenticating Requests: Using Query Parameters".
  const url = presign({
    method: 'GET', url: new URL('https://examplebucket.s3.amazonaws.com/test.txt'), region: 'us-east-1',
    accessKeyId: 'AKIAIOSFODNN7EXAMPLE', secretAccessKey: 'wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY',
    expires: 86400, now: new Date('2013-05-24T00:00:00Z'),
  })
  assert.equal(url, 'https://examplebucket.s3.amazonaws.com/test.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=20130524T000000Z&X-Amz-Expires=86400&X-Amz-SignedHeaders=host&X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404')
})

test('discovers complete bucket bindings only', () => {
  const env = {
    GREGALE_S3_UPLOADS_ENDPOINT: 'https://s3.gregale.dev', GREGALE_S3_UPLOADS_REGION: 'us-east-1', GREGALE_S3_UPLOADS_BUCKET: 'b-123',
    GREGALE_S3_UPLOADS_ACCESS_KEY_ID: 'AK', GREGALE_S3_UPLOADS_SECRET_ACCESS_KEY: 'SK', GREGALE_S3_UPLOADS_ADDRESSING_STYLE: 'path',
    GREGALE_S3_PARTIAL_ENDPOINT: 'https://s3.gregale.dev',
    GREGALE_S3_PLAIN_ENDPOINT: 'http://evil.example', GREGALE_S3_PLAIN_REGION: 'r', GREGALE_S3_PLAIN_BUCKET: 'b', GREGALE_S3_PLAIN_ACCESS_KEY_ID: 'a', GREGALE_S3_PLAIN_SECRET_ACCESS_KEY: 's',
  }
  const bindings = storageBindings(env)
  assert.deepEqual([...bindings.keys()], ['uploads'])
  assert.equal(bindings.get('uploads').bucket, 'b-123')
  assert.equal(bindings.get('uploads').virtualHosted, false)
})

test('names objects by organization and rejects ambiguous keys', () => {
  const org = '6f1c1a52-34c0-4a8e-9c55-2f5d7c0e4a11'
  assert.equal(objectResource('uploads', `org/${org}/reports/q3.pdf`), `org/${org}/uploads/reports/q3.pdf`)
  for (const key of [`org/${org}/`, `org/${org}/a//b`, `org/${org}/../x`, `org/${org}/./x`, 'reports/q3.pdf', `org/${org.toUpperCase()}/x`, `org/${org}/a\\b`, `org/${org}/a\nb`, `org/${org}/${'x'.repeat(1100)}`, 42]) {
    assert.equal(objectResource('uploads', key), null, String(key).slice(0, 60))
  }
})

test('builds path-style and virtual-hosted object URLs with S3 encoding', () => {
  const binding = { endpoint: new URL('https://s3.gregale.dev'), bucket: 'b-123', virtualHosted: false }
  assert.equal(objectURL(binding, "org/x/a b/ü(1)!.txt").toString(), 'https://s3.gregale.dev/b-123/org/x/a%20b/%C3%BC%281%29%21.txt')
  assert.equal(objectURL({ ...binding, virtualHosted: true }, 'org/x/f').toString(), 'https://b-123.s3.gregale.dev/org/x/f')
})
