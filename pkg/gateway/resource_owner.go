package gateway

func (a App) rateOwnerID() string {
	if a.ResourceOwnerID != "" {
		return a.ResourceOwnerID
	}
	return a.AccountID
}
