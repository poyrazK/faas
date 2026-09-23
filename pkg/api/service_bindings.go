package api

// AppServiceBinding is one repository-declared dependency from the returned
// app to another app in the same account. Binding is the platform-owned
// environment key injected into the caller; Service is the target app's
// stable internal name.
//
// This read model records declaration and discovery only. Gateway enforcement
// remains a separate, opt-in policy so existing same-account service calls do
// not change behavior when this field first appears.
type AppServiceBinding struct {
	Binding string `json:"binding"`
	Service string `json:"service"`
}
