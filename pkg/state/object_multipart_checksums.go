package state

import "github.com/onebox-faas/faas/pkg/api"

func validMultipartChecksumCompletion(u ObjectMultipartUpload, parts []api.ObjectMultipartCompletedPart, size int64) bool {
	if !u.Checksum.Complete() {
		return false
	}
	if u.Checksum.Empty() {
		return true
	}
	if len(parts) != len(u.Checksum.Parts) {
		return false
	}
	var total int64
	for i, p := range u.Checksum.Parts {
		if p.Number != parts[i].PartNumber {
			return false
		}
		total += p.Size
	}
	return total == size
}
