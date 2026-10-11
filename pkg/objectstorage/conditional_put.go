package objectstorage

// ValidateConditionalPut checks capability before a signed URL is admitted.
// Native observations happen later, immediately before gateway dispatch.
func ValidateConditionalPut(p Provider, c ObjectWriteConditions) error {
	if !c.Valid() {
		return ErrInvalid
	}
	if c.Empty() {
		return nil
	}
	if _, ok := p.(ConditionalObjectPresigner); !ok {
		return ErrUnsupported
	}
	if _, gcs := p.(*GCS); gcs && !validCopyETagCondition(c.IfMatch) {
		return ErrUnsupported
	}
	return nil
}

// Conditions may come from a persisted public capability or the S3 request.
// An explicit provider argument must never weaken the saved capability.
func signWriteConditions(r SignRequest, c ObjectWriteConditions) (ObjectWriteConditions, error) {
	saved := ObjectWriteConditions{IfMatch: r.IfMatch, IfNoneMatch: r.IfNoneMatch}
	if !saved.Empty() {
		if !c.Empty() && c != saved {
			return c, ErrInvalid
		}
		c = saved
	}
	if !c.Valid() {
		return c, ErrInvalid
	}
	return c, nil
}
