package api

import (
	"fmt"
	"regexp"
	"unicode/utf8"
)

// CodeProductBillingDetailsUnavailable reports that the product team does
// not bill this customer through a Paddle connection that can keep billing
// details (ADR-20261020120000).
const CodeProductBillingDetailsUnavailable = "product_billing_details_unavailable"

// ProductBillingDetails is where a Product Control customer is invoiced. It
// is kept only in the owner's billing provider, never in Gregale
// (ADR-20261020120000).
type ProductBillingDetails struct {
	Provider string                  `json:"provider"`
	Address  *ProductBillingAddress  `json:"address,omitempty"`
	Business *ProductBillingBusiness `json:"business,omitempty"`
}

// ProductBillingAddress is a postal address; country_code is ISO 3166-1
// alpha-2.
type ProductBillingAddress struct {
	CountryCode string `json:"country_code"`
	PostalCode  string `json:"postal_code,omitempty"`
	Region      string `json:"region,omitempty"`
	City        string `json:"city,omitempty"`
	FirstLine   string `json:"first_line,omitempty"`
	SecondLine  string `json:"second_line,omitempty"`
}

// ProductBillingBusiness names the company invoiced and its tax identifier.
type ProductBillingBusiness struct {
	Name          string `json:"name"`
	TaxIdentifier string `json:"tax_identifier,omitempty"`
}

// PutProductBillingDetailsRequest sets the address and, when present, the
// business; an omitted business is left unchanged.
type PutProductBillingDetailsRequest struct {
	Address  ProductBillingAddress   `json:"address"`
	Business *ProductBillingBusiness `json:"business,omitempty"`
}

var countryCodeRE = regexp.MustCompile(`^[A-Z]{2}$`)

// Validate checks field sizes and the country code.
func (r PutProductBillingDetailsRequest) Validate() error {
	if !countryCodeRE.MatchString(r.Address.CountryCode) {
		return fmt.Errorf("address.country_code must be a two-letter ISO country code")
	}
	fields := map[string]string{"address.postal_code": r.Address.PostalCode, "address.region": r.Address.Region, "address.city": r.Address.City,
		"address.first_line": r.Address.FirstLine, "address.second_line": r.Address.SecondLine}
	if r.Business != nil {
		if r.Business.Name == "" {
			return fmt.Errorf("business.name is required with a business")
		}
		fields["business.name"], fields["business.tax_identifier"] = r.Business.Name, r.Business.TaxIdentifier
	}
	for name, value := range fields {
		if len(value) > ProductBillingDetailsFieldMaxBytes || !utf8.ValidString(value) {
			return fmt.Errorf("%s must be valid UTF-8 of at most %d bytes", name, ProductBillingDetailsFieldMaxBytes)
		}
	}
	return nil
}
