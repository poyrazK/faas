package internalmail

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *Service) registerOperations(mux *http.ServeMux) {
	s.registerDeliveryControls(mux)
	s.registerDomainLifecycle(mux)
	mux.HandleFunc("GET "+OperationsPath+"/messages", s.getOperatorMessages)
	mux.HandleFunc("GET "+OperationsPath+"/messages/{id}", s.getOperatorMessage)
	mux.HandleFunc("GET "+OperationsPath+"/messages/{id}/events", s.getOperatorEvents)
	mux.HandleFunc("GET "+OperationsPath+"/domains", s.getOperatorDomains)
	mux.HandleFunc("GET "+OperationsPath+"/domains/{id}", s.getOperatorDomain)
	mux.HandleFunc("GET "+OperationsPath+"/domains/{id}/check", s.getOperatorDomainCheck)
	mux.HandleFunc("GET "+OperationsPath+"/suppressions", s.getOperatorSuppressions)
	mux.HandleFunc("GET "+OperationsPath+"/audit", s.getOperatorAudit)
	mux.HandleFunc("POST "+OperationsPath+"/recipients/{id}/reconcile", s.postOperatorReconcile)
	mux.HandleFunc("POST "+OperationsPath+"/retention/preview", s.postOperatorPreview)
	mux.HandleFunc("POST "+OperationsPath+"/retention/apply", s.postOperatorRetention)
}

func operatorList(r *http.Request, uuidCursor, statusFilter bool) (OperatorList, error) {
	o := OperatorList{Limit: api.InternalMailOperatorPageSize}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return o, ErrInvalid
	}
	for name, v := range values {
		if len(v) != 1 {
			return o, ErrInvalid
		}
		switch name {
		case "limit":
			o.Limit, err = strconv.Atoi(v[0])
		case "after":
			o.After = v[0]
		case "status":
			if !statusFilter {
				return o, ErrInvalid
			}
			o.Status = v[0]
		default:
			return o, ErrInvalid
		}
		if err != nil {
			return o, ErrInvalid
		}
	}
	return o, o.validate(uuidCursor)
}

func operatorRead[T any](s *Service, w http.ResponseWriter, r *http.Request, uuidCursor, statusFilter bool, read func(context.Context, operationsStore, OperatorList) (T, error)) {
	store, err := s.operationStore()
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	o, err := operatorList(r, uuidCursor, statusFilter)
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	value, err := read(r.Context(), store, o)
	respond(w, value, err, http.StatusOK)
}

func (s *Service) getOperatorMessages(w http.ResponseWriter, r *http.Request) {
	operatorRead(s, w, r, true, true, func(ctx context.Context, store operationsStore, o OperatorList) (OperatorPage[OperatorMessage], error) {
		return store.operatorMessages(ctx, o)
	})
}
func (s *Service) getOperatorDomains(w http.ResponseWriter, r *http.Request) {
	operatorRead(s, w, r, true, false, func(ctx context.Context, store operationsStore, o OperatorList) (OperatorPage[Domain], error) {
		return store.operatorDomains(ctx, o)
	})
}
func (s *Service) getOperatorSuppressions(w http.ResponseWriter, r *http.Request) {
	operatorRead(s, w, r, false, false, func(ctx context.Context, store operationsStore, o OperatorList) (OperatorPage[Suppression], error) {
		return store.operatorSuppressions(ctx, o)
	})
}
func (s *Service) getOperatorAudit(w http.ResponseWriter, r *http.Request) {
	operatorRead(s, w, r, true, false, func(ctx context.Context, store operationsStore, o OperatorList) (OperatorPage[OperationRecord], error) {
		return store.operatorAudit(ctx, o)
	})
}
func (s *Service) getOperatorEvents(w http.ResponseWriter, r *http.Request) {
	if !validOperatorID(w, r) {
		return
	}
	operatorRead(s, w, r, false, false, func(ctx context.Context, store operationsStore, o OperatorList) (OperatorPage[Event], error) {
		return store.operatorEvents(ctx, r.PathValue("id"), o)
	})
}
func (s *Service) getOperatorMessage(w http.ResponseWriter, r *http.Request) {
	if !validOperatorID(w, r) {
		return
	}
	store, err := s.operationStore()
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	m, err := store.operatorMessage(r.Context(), r.PathValue("id"))
	respond(w, m, err, http.StatusOK)
}
func (s *Service) getOperatorDomain(w http.ResponseWriter, r *http.Request) {
	if !validOperatorID(w, r) {
		return
	}
	store, err := s.operationStore()
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	d, err := store.operatorDomain(r.Context(), r.PathValue("id"))
	respond(w, s.domainResponse(d), err, http.StatusOK)
}

type OperatorDNSRecord struct {
	DNSRecord
	Status string `json:"status"`
}
type OperatorDomainCheck struct {
	DomainID     string              `json:"domain_id"`
	DomainStatus string              `json:"domain_status"`
	DNSReady     bool                `json:"dns_ready"`
	Records      []OperatorDNSRecord `json:"records"`
}

func (s *Service) getOperatorDomainCheck(w http.ResponseWriter, r *http.Request) {
	if !validOperatorID(w, r) {
		return
	}
	store, err := s.operationStore()
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	d, err := store.operatorDomain(r.Context(), r.PathValue("id"))
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	result := s.checkOperatorDNS(r.Context(), d)
	respond(w, result, nil, http.StatusOK)
}

// DNS readiness is an observation. This read never changes domain status or
// overrides disabled status; activation remains a separate service operation.
func (s *Service) checkOperatorDNS(ctx context.Context, d Domain) OperatorDomainCheck {
	ctx, cancel := context.WithTimeout(ctx, api.InternalMailDNSVerifyTimeout)
	defer cancel()
	result := OperatorDomainCheck{DomainID: d.ID, DomainStatus: d.Status, DNSReady: true, Records: []OperatorDNSRecord{}}
	for i, record := range s.domainResponse(d).Records {
		status := "lookup_failed"
		if s.Resolver != nil {
			values, err := s.Resolver.LookupTXT(ctx, record.Name)
			if err == nil {
				status = "missing_or_mismatched"
				if s.recordMatches(d, record, i, values) {
					status = "matched"
				}
			}
		}
		result.Records = append(result.Records, OperatorDNSRecord{DNSRecord: record, Status: status})
		result.DNSReady = result.DNSReady && status == "matched"
	}
	return result
}

func (s *Service) postOperatorReconcile(w http.ResponseWriter, r *http.Request) {
	if !validOperatorID(w, r) {
		return
	}
	var input ReconcileRequest
	if !decode(w, r, &input) {
		return
	}
	record, err := s.ReconcileUncertain(r.Context(), r.PathValue("id"), input)
	respond(w, record, err, http.StatusOK)
}
func (s *Service) postOperatorPreview(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !decode(w, r, &input) {
		return
	}
	result, err := s.RetainPayloads(r.Context(), true)
	respond(w, result, err, http.StatusOK)
}
func (s *Service) postOperatorRetention(w http.ResponseWriter, r *http.Request) {
	var input OperatorAuditInput
	if !decode(w, r, &input) {
		return
	}
	record, err := s.OperatorRetainPayloads(r.Context(), input)
	respond(w, record, err, http.StatusOK)
}
func validOperatorID(w http.ResponseWriter, r *http.Request) bool {
	if !canonicalOperatorID(r.PathValue("id")) {
		respond(w, nil, ErrInvalid, 0)
		return false
	}
	return true
}
