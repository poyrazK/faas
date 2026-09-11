package s3gateway

import (
	"encoding/xml"
	"net/http"
	"sort"
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
	CommonPrefixes        []commonPrefix `xml:"CommonPrefixes,omitempty"`
	NextContinuationToken string         `xml:"NextContinuationToken,omitempty"`
}

type listedObject struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

type commonPrefix struct {
	Prefix string `xml:"Prefix"`
}

type initiateMultipartResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	XMLNS    string   `xml:"xmlns,attr"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadID string   `xml:"UploadId"`
}

type listedMultipartUpload struct {
	Key       string `xml:"Key"`
	UploadID  string `xml:"UploadId"`
	Initiated string `xml:"Initiated"`
}

type listMultipartUploadsResult struct {
	XMLName          xml.Name                `xml:"ListMultipartUploadsResult"`
	XMLNS            string                  `xml:"xmlns,attr"`
	Bucket           string                  `xml:"Bucket"`
	KeyMarker        string                  `xml:"KeyMarker,omitempty"`
	UploadMarker     string                  `xml:"UploadIdMarker,omitempty"`
	NextKeyMarker    string                  `xml:"NextKeyMarker,omitempty"`
	NextUploadMarker string                  `xml:"NextUploadIdMarker,omitempty"`
	Prefix           string                  `xml:"Prefix,omitempty"`
	MaxUploads       int32                   `xml:"MaxUploads"`
	IsTruncated      bool                    `xml:"IsTruncated"`
	Uploads          []listedMultipartUpload `xml:"Upload,omitempty"`
}

type listedMultipartPart struct {
	PartNumber   int32  `xml:"PartNumber"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
}

type listMultipartPartsResult struct {
	XMLName              xml.Name              `xml:"ListPartsResult"`
	XMLNS                string                `xml:"xmlns,attr"`
	Bucket               string                `xml:"Bucket"`
	Key                  string                `xml:"Key"`
	UploadID             string                `xml:"UploadId"`
	PartNumberMarker     int32                 `xml:"PartNumberMarker"`
	NextPartNumberMarker int32                 `xml:"NextPartNumberMarker,omitempty"`
	MaxParts             int32                 `xml:"MaxParts"`
	IsTruncated          bool                  `xml:"IsTruncated"`
	Parts                []listedMultipartPart `xml:"Part,omitempty"`
}

type completeMultipartResult struct {
	XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
	XMLNS    string   `xml:"xmlns,attr"`
	Location string   `xml:"Location,omitempty"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag,omitempty"`
	UploadID string   `xml:"UploadId,omitempty"`
}

type copyObjectResult struct {
	XMLName      xml.Name `xml:"CopyObjectResult"`
	XMLNS        string   `xml:"xmlns,attr"`
	LastModified string   `xml:"LastModified,omitempty"`
	ETag         string   `xml:"ETag"`
}

type objectTaggingRequest struct {
	XMLName xml.Name    `xml:"Tagging"`
	Tags    []objectTag `xml:"TagSet>Tag"`
}

type objectTaggingResult struct {
	XMLName xml.Name    `xml:"Tagging"`
	XMLNS   string      `xml:"xmlns,attr"`
	Tags    []objectTag `xml:"TagSet>Tag"`
}

type objectTag struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

func objectTagSet(tags map[string]string) []objectTag {
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]objectTag, 0, len(keys))
	for _, key := range keys {
		out = append(out, objectTag{Key: key, Value: tags[key]})
	}
	return out
}

func listObjectsResult(bucket, prefix string, limit int32, page objectstorage.ObjectPage) listBucketResult {
	result := listBucketResult{
		XMLNS: s3XMLNamespace, Name: bucket, Prefix: prefix, KeyCount: len(page.Items) + len(page.CommonPrefixes), MaxKeys: limit,
		IsTruncated: page.NextCursor != "", NextContinuationToken: page.NextCursor,
		Contents:       make([]listedObject, 0, len(page.Items)),
		CommonPrefixes: make([]commonPrefix, 0, len(page.CommonPrefixes)),
	}
	for _, object := range page.Items {
		result.Contents = append(result.Contents, listedObject{
			Key: object.Key, LastModified: object.LastModified.UTC().Format(time.RFC3339Nano),
			Size: object.Size, StorageClass: "STANDARD",
		})
	}
	for _, value := range page.CommonPrefixes {
		result.CommonPrefixes = append(result.CommonPrefixes, commonPrefix{Prefix: value})
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
