package api

// ObjectWriteProtection selects fixed retention and an independent legal hold
// for a new version. An omitted retention inherits the admitted bucket default.
type ObjectWriteProtection struct {
	Retention *ObjectVersionRetention `json:"retention,omitempty"`
	LegalHold *ObjectVersionLegalHold `json:"legal_hold,omitempty"`
}

func (p ObjectWriteProtection) Empty() bool { return p.Retention == nil && p.LegalHold == nil }
func (p ObjectWriteProtection) Valid() bool {
	return (p.Retention == nil || p.Retention.Valid() && !p.Retention.Empty() && p.Retention.EventHold == "" && p.Retention.EventHoldDuration == nil && p.Retention.RetainUntilDate != nil) && (p.LegalHold == nil || p.LegalHold.Valid())
}
func (p ObjectWriteProtection) Clone() ObjectWriteProtection {
	if p.Retention != nil {
		r := p.Retention.Clone()
		p.Retention = &r
	}
	if p.LegalHold != nil {
		h := *p.LegalHold
		p.LegalHold = &h
	}
	return p
}
func (p ObjectWriteProtection) ForWrite() ObjectWriteProtection {
	p = p.Clone()
	if p.Retention != nil {
		r := p.Retention.ForWrite()
		p.Retention = &r
	}
	return p
}
