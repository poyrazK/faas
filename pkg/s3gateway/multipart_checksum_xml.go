package s3gateway

import (
	"encoding/xml"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/objectchecksum"
)

// An authenticated XML body must have one unambiguous selection per part.
// encoding/xml's ordinary struct decoder otherwise accepts repeated fields.
func (p *completeMultipartPart) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	seen := map[string]bool{}
	for {
		token, err := d.Token()
		if err != nil {
			return err
		}
		switch element := token.(type) {
		case xml.StartElement:
			name := element.Name.Local
			if seen[name] || len(element.Attr) != 0 {
				return objectchecksum.ErrInvalid
			}
			seen[name] = true
			var value string
			if err = d.DecodeElement(&value, &element); err != nil {
				return err
			}
			switch name {
			case "PartNumber":
				n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
				if err != nil {
					return err
				}
				p.PartNumber = int32(n)
			case "ETag":
				p.ETag = value
			case "ChecksumCRC32":
				p.CRC32 = value
			case "ChecksumCRC32C":
				p.CRC32C = value
			case "ChecksumCRC64NVME":
				p.CRC64NVME = value
			case "ChecksumSHA1":
				p.SHA1 = value
			case "ChecksumSHA256":
				p.SHA256 = value
			default:
				return objectchecksum.ErrInvalid
			}
		case xml.EndElement:
			if element.Name == start.Name {
				return nil
			}
		case xml.CharData:
			if strings.TrimSpace(string(element)) != "" {
				return objectchecksum.ErrInvalid
			}
		}
	}
}
