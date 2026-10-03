package objectstorage

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 410
func TestObjectNotificationsXMLStrictAndRoundTrip(t *testing.T) {
	arn := "arn:gregale:sqs:us-east-1:11111111-1111-4111-8111-111111111111:22222222-2222-4222-8222-222222222222/storage"
	good := []api.ObjectNotificationRule{{ID: "queue", Destination: arn, Events: []string{"s3:ObjectCreated:Put"}, Prefix: "images/red +%/", Suffix: ".jpg"}}
	body, err := MarshalObjectNotificationsXML(good)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseObjectNotificationsXML(body)
	if err != nil || !reflect.DeepEqual(parsed, good) {
		t.Fatal(string(body), parsed, err)
	}
	if _, e := ParseObjectNotificationsXML([]byte(`<NotificationConfiguration/>`)); e != nil {
		t.Fatal("empty intent", e)
	}
	base := `<NotificationConfiguration><QueueConfiguration><Id>queue</Id><Queue>` + arn + `</Queue><Event>s3:ObjectCreated:Put</Event></QueueConfiguration></NotificationConfiguration>`
	for _, body := range []string{base + `<extra/>`, strings.Replace(base, "<Id>queue</Id>", "<Id>queue</Id><Id>other</Id>", 1), strings.Replace(base, "<Id>queue</Id>", "<Unknown>quietly-lost</Unknown>", 1), strings.Replace(base, "<Queue>", "<Queue x='ignored'>", 1), strings.Replace(base, "</QueueConfiguration>", "<Filter><S3Key><FilterRule><Name>prefix</Name><Value>%bad%</Value></FilterRule></S3Key></Filter></QueueConfiguration>", 1), `<!DOCTYPE n [<!ENTITY x 'inject'>]>` + base, strings.Repeat("a", int(api.MaxObjectNotificationBodyBytes)+1)} {
		if _, e := ParseObjectNotificationsXML([]byte(body)); e == nil {
			t.Fatal("malformed XML accepted", body[:min(len(body), 120)])
		}
	}
	for _, field := range []string{"TopicConfiguration", "EventBridgeConfiguration"} {
		if _, e := ParseObjectNotificationsXML([]byte(`<NotificationConfiguration><` + field + `/></NotificationConfiguration>`)); !errors.Is(e, ErrUnsupported) {
			t.Fatal(field, e)
		}
	}
}
