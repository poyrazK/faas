package s3gateway

import (
	"bytes"
	"encoding/xml"
	"io"
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
	Delimiter             string         `xml:"Delimiter,omitempty"`
	ContinuationToken     string         `xml:"ContinuationToken,omitempty"`
	StartAfter            string         `xml:"StartAfter,omitempty"`
	EncodingType          string         `xml:"EncodingType,omitempty"`
	NextContinuationToken string         `xml:"NextContinuationToken,omitempty"`
}

type listedObject struct {
	ETag         string `xml:"ETag,omitempty"`
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
	EncodingType     string                  `xml:"EncodingType,omitempty"`
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
	checksumXML
	PartNumber   int32  `xml:"PartNumber"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
}

type listMultipartPartsResult struct {
	ChecksumAlgorithm    string                `xml:"ChecksumAlgorithm,omitempty"`
	ChecksumType         string                `xml:"ChecksumType,omitempty"`
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
	checksumXML
	ChecksumType string   `xml:"ChecksumType,omitempty"`
	XMLName      xml.Name `xml:"CompleteMultipartUploadResult"`
	XMLNS        string   `xml:"xmlns,attr"`
	Location     string   `xml:"Location,omitempty"`
	Bucket       string   `xml:"Bucket"`
	Key          string   `xml:"Key"`
	ETag         string   `xml:"ETag,omitempty"`
	UploadID     string   `xml:"UploadId,omitempty"`
}

type copyObjectResult struct {
	XMLName      xml.Name `xml:"CopyObjectResult"`
	XMLNS        string   `xml:"xmlns,attr"`
	LastModified string   `xml:"LastModified,omitempty"`
	ETag         string   `xml:"ETag"`
}

type objectTaggingResult struct {
	XMLName xml.Name    `xml:"Tagging"`
	XMLNS   string      `xml:"xmlns,attr"`
	Tags    []objectTag `xml:"TagSet>Tag"`
}

// Empty tags still require a TagSet element in the S3 response document.
func (v objectTaggingResult) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	var wire struct {
		XMLNS string `xml:"xmlns,attr,omitempty"`
		Set   struct {
			Tags []objectTag `xml:"Tag"`
		} `xml:"TagSet"`
	}
	wire.XMLNS, wire.Set.Tags = v.XMLNS, v.Tags
	start.Name = xml.Name{Local: "Tagging"}
	return e.EncodeElement(wire, start)
}

type objectTag struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type deleteObjectsRequest struct {
	XMLName xml.Name             `xml:"Delete"`
	Objects []deleteObjectTarget `xml:"Object"`
	Quiet   bool                 `xml:"Quiet"`
}

type deleteObjectTarget struct {
	Key       string `xml:"Key"`
	VersionID string `xml:"VersionId"`
}

type deleteObjectsResult struct {
	XMLName xml.Name              `xml:"DeleteResult"`
	XMLNS   string                `xml:"xmlns,attr"`
	Deleted []deletedObjectResult `xml:"Deleted,omitempty"`
	Errors  []deleteObjectError   `xml:"Error,omitempty"`
}

type deletedObjectResult struct {
	DeleteMarkerVersionID string `xml:"DeleteMarkerVersionId,omitempty"`
	Key                   string `xml:"Key"`
	VersionID             string `xml:"VersionId,omitempty"`
	DeleteMarker          bool   `xml:"DeleteMarker,omitempty"`
}

type deleteObjectError struct {
	Key       string `xml:"Key"`
	VersionID string `xml:"VersionId,omitempty"`
	Code      string `xml:"Code"`
	Message   string `xml:"Message"`
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
			ETag: object.ETag, Size: object.Size, StorageClass: "STANDARD",
		})
	}
	for _, value := range page.CommonPrefixes {
		result.CommonPrefixes = append(result.CommonPrefixes, commonPrefix{Prefix: value})
	}
	return result
}

// writeListingXML sends a listing as egress, like an object body: its bytes
// count against the account's monthly egress (reserved before sending under
// gateway safety, recorded after it otherwise) and are paced by the node's
// transfer share (ADR-20261031122757). Listings wrote straight to the client,
// so a page of long keys bypassed both (bug hunt 33).
func (h *Handler) writeListingXML(w http.ResponseWriter, r *http.Request, req requestContext, value any) {
	var body bytes.Buffer
	body.WriteString(xml.Header)
	if err := xml.NewEncoder(&body).Encode(value); err != nil {
		writeS3Error(w, http.StatusInternalServerError, "InternalError", "The listing could not be encoded.", r.URL.Path, req.requestID)
		return
	}
	now := h.now().UTC()
	safety := h.registry.Accounting.GatewaySafety()
	if safety && !h.writeAdmissionError(w, r, req, objectstorage.ReserveGatewayBytes(r.Context(), h.requestMetrics, req.bucket.ID, int64(body.Len()), h.registry.Accounting, now)) {
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("x-amz-request-id", req.requestID)
	w.WriteHeader(http.StatusOK)
	n, _ := io.Copy(w, h.transfers.Reader(r.Context(), &body, objectstorage.TransferDownload))
	if !safety {
		if err := objectstorage.RecordDeliveredEgress(r.Context(), h.requestMetrics, req.bucket.ID, n, now); err != nil {
			h.log.Warn("S3 egress metric write failed", "request_id", req.requestID)
		}
	}
}

func writeS3XML(w http.ResponseWriter, status int, requestID string, value any) {
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("x-amz-request-id", requestID)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(value)
}
