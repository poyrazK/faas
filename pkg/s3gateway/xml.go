package s3gateway

import (
	"encoding/xml"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
)

const s3XMLNamespace = "http://s3.amazonaws.com/doc/2006-03-01/"

type errorResult struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource,omitempty"`
	RequestID string   `xml:"RequestId"`
}

func writeS3Error(w http.ResponseWriter, status int, code, message, resource, requestID string) {
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("x-amz-request-id", requestID)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(errorResult{Code: code, Message: message, Resource: resource, RequestID: requestID})
}

type listAllMyBucketsResult struct {
	XMLName xml.Name        `xml:"ListAllMyBucketsResult"`
	XMLNS   string          `xml:"xmlns,attr"`
	Buckets listBucketsBody `xml:"Buckets"`
}

type listBucketsBody struct {
	Buckets []listBucket `xml:"Bucket"`
}

type listBucket struct {
	Name         string `xml:"Name"`
	CreationDate string `xml:"CreationDate"`
}

type listBucketResult struct {
	XMLName               xml.Name       `xml:"ListBucketResult"`
	XMLNS                 string         `xml:"xmlns,attr"`
	Name                  string         `xml:"Name"`
	Prefix                string         `xml:"Prefix"`
	KeyCount              int            `xml:"KeyCount"`
	MaxKeys               int32          `xml:"MaxKeys"`
	IsTruncated           bool           `xml:"IsTruncated"`
	Contents              []listedObject `xml:"Contents"`
	NextContinuationToken string         `xml:"NextContinuationToken,omitempty"`
}

type listedObject struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

func listObjectsResult(bucket, prefix string, limit int32, page objectstorage.ObjectPage) listBucketResult {
	result := listBucketResult{
		XMLNS: s3XMLNamespace, Name: bucket, Prefix: prefix, KeyCount: len(page.Items), MaxKeys: limit,
		IsTruncated: page.NextCursor != "", NextContinuationToken: page.NextCursor,
		Contents: make([]listedObject, 0, len(page.Items)),
	}
	for _, object := range page.Items {
		result.Contents = append(result.Contents, listedObject{
			Key: object.Key, LastModified: object.LastModified.UTC().Format(time.RFC3339Nano),
			Size: object.Size, StorageClass: "STANDARD",
		})
	}
	return result
}

func writeS3XML(w http.ResponseWriter, status int, requestID string, value any) {
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("x-amz-request-id", requestID)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(value)
}
