package product

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) helpOwnerContext(w http.ResponseWriter, r *http.Request, acct state.Account) (*ownerContext, state.ProductHelpStore, bool) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return nil, nil, false
	}
	store, ok := s.Host.Store().(state.ProductHelpStore)
	if !ok {
		s.ownerError(w, r, c, state.ErrNotFound)
		return c, nil, false
	}
	return c, store, true
}

func (s *Service) OwnerHelp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, store, ok := s.helpOwnerContext(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "tenant", "status", "cursor")
	if err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	ctx, cancel := helpReadContext(r)
	defer cancel()
	status := helpStatusFilter(q.Get("status"), "active")
	page, err := store.ListProductOwnerHelp(ctx, acct.ID, c.app.ID, q.Get("tenant"), status, q.Get("cursor"))
	if err = helpScoped(r, page, acct.ID, c.app.ID, q.Get("tenant"), err); err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	c.data.TenantID = q.Get("tenant")
	c.data.Help = ownerHelpQueue(c, page, status)
	s.OwnerRender(w, r, acct, c.data)
}

func ownerHelpQueue(c *ownerContext, page api.ProductHelpPage, status string) *dashboard.ProductControlHelp {
	v := &dashboard.ProductControlHelp{Status: status}
	for _, record := range page.Requests {
		text := []rune(record.Request.Message)
		if len(text) > 160 {
			text = append(text[:160], '…')
		}
		v.Queue = append(v.Queue, dashboard.ProductControlHelpRow{ProductHelpRequest: record, URL: c.data.BaseURL + "/help/" + record.Request.ID, Preview: string(text)})
	}
	if page.NextCursor != "" {
		v.NextURL = c.data.BaseURL + "/help?" + url.Values{"tenant": {c.data.TenantID}, "status": {status}, "cursor": {page.NextCursor}}.Encode()
	}
	return v
}

func (s *Service) OwnerHelpRequest(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, store, ok := s.helpOwnerContext(w, r, acct)
	if !ok {
		return
	}
	if _, err := ownerQuery(r); err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	ctx, cancel := helpReadContext(r)
	defer cancel()
	value, err := store.GetProductOwnerHelp(ctx, acct.ID, c.app.ID, r.PathValue("request"))
	if err = helpScoped(r, value, acct.ID, c.app.ID, "", err); err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	token, err := s.ownerToken(w, c, "help_update", acct.ID+"/"+c.app.ID+"/"+value.Request.ID)
	if err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	raw, _ := json.MarshalIndent(value.Context, "", "  ")
	c.data.TenantID = value.TenantID
	c.data.Help = &dashboard.ProductControlHelp{Request: &value, ContextJSON: string(raw), Token: token, TextMaxBytes: api.ProductControlHelpTextMaxBytes}
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) OwnerHelpUpdate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, store, ok := s.helpOwnerContext(w, r, acct)
	if !ok {
		return
	}
	f, err := ownerParseFormLimit(w, r, []string{"csrf_token", "version", "status", "response"}, api.ProductControlHelpBodyMaxBytes)
	if err == nil {
		err = s.ownerVerify(r, c, "help_update", acct.ID+"/"+c.app.ID+"/"+r.PathValue("request"))
	}
	var version int64
	if err == nil {
		version, err = strconv.ParseInt(f.Get("version"), 10, 64)
		if err != nil {
			err = state.ErrInvalidArgument
		}
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	ctx, cancel := helpReadContext(r)
	defer cancel()
	_, err = store.UpdateProductHelp(ctx, acct.ID, c.app.ID, r.PathValue("request"), api.ProductHelpUpdate{ExpectedVersion: version, Status: f.Get("status"), Response: f.Get("response")})
	if err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	http.Redirect(w, r, c.data.BaseURL+"/help/"+r.PathValue("request"), http.StatusSeeOther)
}
