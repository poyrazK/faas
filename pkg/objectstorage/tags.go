package objectstorage

import (
	"net/url"
	"strings"
)

// ParseObjectTags decodes the URL-encoded value used by x-amz-tagging. It is
// deliberately stricter than url.ParseQuery: duplicate keys and malformed
// pairs are ambiguous when a request is signed and therefore rejected.
func ParseObjectTags(raw string) (map[string]string, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > maxObjectTaggingBytes {
		return nil, ErrInvalid
	}
	tags := make(map[string]string)
	for _, pair := range strings.Split(raw, "&") {
		if pair == "" {
			return nil, ErrInvalid
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return nil, ErrInvalid
		}
		key, err := url.QueryUnescape(parts[0])
		if err != nil {
			return nil, ErrInvalid
		}
		value, err := url.QueryUnescape(parts[1])
		if err != nil {
			return nil, ErrInvalid
		}
		if _, exists := tags[key]; exists {
			return nil, ErrInvalid
		}
		tags[key] = value
	}
	if err := ValidateObjectMetadata(ObjectMetadata{Tags: tags}); err != nil {
		return nil, err
	}
	return tags, nil
}

// EncodeObjectTags returns the canonical URL-encoded S3 tagging form.
func EncodeObjectTags(tags map[string]string) (string, error) {
	if err := ValidateObjectMetadata(ObjectMetadata{Tags: tags}); err != nil {
		return "", err
	}
	if len(tags) == 0 {
		return "", nil
	}
	values := make(url.Values, len(tags))
	for key, value := range tags {
		values.Set(key, value)
	}
	return values.Encode(), nil
}
