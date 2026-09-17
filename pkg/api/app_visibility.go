package api

// AppVisibility controls whether an app is reachable through the public edge.
// Internal apps remain addressable through authenticated service-to-service
// routing (the *.svc.gregale namespace) but do not receive public routes.
type AppVisibility string

const (
	AppVisibilityPublic   AppVisibility = "public"
	AppVisibilityInternal AppVisibility = "internal"
)

var AppVisibilityClosedSet = []AppVisibility{
	AppVisibilityPublic,
	AppVisibilityInternal,
}

func (v AppVisibility) Valid() bool {
	return v == AppVisibilityPublic || v == AppVisibilityInternal
}

func NormalizeAppVisibility(v AppVisibility) AppVisibility {
	if v == "" {
		return AppVisibilityPublic
	}
	return v
}
