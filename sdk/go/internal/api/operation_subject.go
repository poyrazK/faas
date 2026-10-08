// ADR-714: public operation business references.
package api

type OperationSubjectSpec struct {
	Type   string `json:"type" yaml:"type" toml:"type"`
	IDFrom string `json:"id_from" yaml:"id_from" toml:"id_from"`
}
type OperationSubject struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}
