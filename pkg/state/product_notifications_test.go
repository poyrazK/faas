package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type productNotificationTestStore interface {
	productHelpTestStore
	ProductNotificationStore
}

func notificationTestUpdate(n api.ProductNotification, read bool) api.ProductNotificationUpdate {
	version := n.ReadVersion
	return api.ProductNotificationUpdate{ID: n.ID, Revision: n.Revision, ExpectedVersion: &version, Read: &read}
}

func notificationTestPage(ctx context.Context, t *testing.T, s ProductNotificationStore, scope ProductNotificationScope, filter, cursor string) api.ProductNotificationPage {
	t.Helper()
	p, err := s.ListProductNotifications(ctx, scope, filter, cursor)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func notificationTestReply(ctx context.Context, t *testing.T, s productNotificationTestStore, scope ProductNotificationScope, kind, feature, op string) api.ProductHelpRequest {
	t.Helper()
	r := helpCreate(ctx, t, s, ProductHelpScope(scope), kind, feature, op)
	r, err := s.UpdateProductHelp(ctx, scope.AccountID, scope.AppID, r.Request.ID, api.ProductHelpUpdate{ExpectedVersion: 1, Status: "resolved", Response: "Private owner response"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestProductControlNotificationsMemory(t *testing.T) {
	s := NewMemStore()
	settle := func(op Operation) { s.mu.Lock(); defer s.mu.Unlock(); s.settleProductOperationLocked(op) }
	productNotificationContract(t, s, settle)
}

func TestProductControlNotificationBoundsMemory(t *testing.T) {
	s := NewMemStore()
	seed := func(scope ProductNotificationScope, expires time.Time) {
		s.mu.Lock()
		defer s.mu.Unlock()
		d := s.productLocked()
		d.notificationReads = map[string]productNotificationRead{}
		for range api.ProductControlNotificationMaxReads {
			d.notificationReads[notificationReadKey(scope, "help/"+uuid.NewString())] = productNotificationRead{sourceVersion: "help:2", read: true, version: 1, expires: expires}
		}
	}
	productNotificationBounds(t, s, seed)
	f := helpFixture(t.Context(), t, s)
	scope := ProductNotificationScope{f.scope}
	scope.Role = "operator"
	f.admission.IdempotencyKey = "notification-attention"
	op, _, err := s.AdmitOperation(t.Context(), f.admission)
	if err != nil {
		t.Fatal(err)
	}
	op.State, op.UpdatedAt = api.OperationRequiresReconciliation, time.Now().UTC().Truncate(time.Microsecond)
	s.mu.Lock()
	s.operationMemoryLocked().operations[op.ID] = op
	s.mu.Unlock()
	productNotificationAttention(t, s, scope, op)
}

func productNotificationBounds(t *testing.T, s productNotificationTestStore, seed func(ProductNotificationScope, time.Time)) {
	t.Helper()
	f := helpFixture(t.Context(), t, s)
	scope := ProductNotificationScope{f.scope}
	scope.Role = "operator"
	notificationTestReply(t.Context(), t, s, scope, "workspace", "", "")
	n := notificationTestPage(t.Context(), t, s, scope, "all", "").Items[0]
	seed(scope, time.Now().Add(time.Hour))
	if _, err := s.UpdateProductNotification(t.Context(), scope, notificationTestUpdate(n, false)); err != nil {
		t.Fatal("unchanged acknowledgement at capacity", err)
	}
	if _, err := s.UpdateProductNotification(t.Context(), scope, notificationTestUpdate(n, true)); !errors.Is(err, ErrOperationQuota) {
		t.Fatal("marker capacity not enforced", err)
	}
	if page := notificationTestPage(t.Context(), t, s, scope, "all", ""); page.Items[0].Read || page.UnreadCount != 1 {
		t.Fatal("failed acknowledgement changed read state", page)
	}
	seed(scope, time.Now().Add(-time.Second))
	read, err := s.UpdateProductNotification(t.Context(), scope, notificationTestUpdate(n, true))
	if err != nil || !read.Read || read.ReadVersion != 1 {
		t.Fatal("expired markers did not release capacity", read, err)
	}
	if _, err = s.UpdateProductNotification(t.Context(), scope, notificationTestUpdate(read, false)); err != nil {
		t.Fatal("existing read metadata toggle", err)
	}
}

func productNotificationAttention(t *testing.T, s ProductNotificationStore, scope ProductNotificationScope, op Operation) {
	t.Helper()
	p := notificationTestPage(t.Context(), t, s, scope, "all", "")
	if len(p.Items) != 1 || p.Items[0].Kind != "work_attention" || p.Items[0].ReferenceID != op.ID {
		t.Fatal("attention projection", p)
	}
	read, err := s.UpdateProductNotification(t.Context(), scope, notificationTestUpdate(p.Items[0], true))
	if err != nil || !read.Read {
		t.Fatal("attention acknowledgement", read, err)
	}
}

func productNotificationContract(t *testing.T, s productNotificationTestStore, settle func(Operation)) {
	t.Helper()
	ctx := t.Context()
	t.Run("shared read state, new replies and stale acknowledgements", func(t *testing.T) {
		f := helpFixture(ctx, t, s)
		scope := ProductNotificationScope{f.scope}
		scope.Role = "operator"
		before, err := s.GetProductUsage(ctx, scope.AccountID, scope.AppID, scope.TenantID, productcontrol.Period(time.Now()))
		if err != nil {
			t.Fatal(err)
		}
		r := notificationTestReply(ctx, t, s, scope, "workspace", "", "")
		p := notificationTestPage(ctx, t, s, scope, "all", "")
		if len(p.Items) != 1 || p.UnreadCount != 1 || p.ReadStateScope != "workspace" {
			t.Fatalf("new update: %+v", p)
		}
		n := p.Items[0]
		req := notificationTestUpdate(n, true)
		read, err := s.UpdateProductNotification(ctx, scope, req)
		if err != nil || !read.Read || read.ReadVersion != 1 {
			t.Fatal("read", read, err)
		}
		again, err := s.UpdateProductNotification(ctx, scope, req)
		if err != nil || again != read {
			t.Fatal("lost acknowledgement retry", again, err)
		}
		other := scope
		other.Role = "team"
		p = notificationTestPage(ctx, t, s, other, "all", "")
		if !p.Items[0].Read || p.UnreadCount != 0 {
			t.Fatal("read state was not shared")
		}
		unread, err := s.UpdateProductNotification(ctx, scope, notificationTestUpdate(read, false))
		if err != nil || unread.ReadVersion != 2 || unread.Read {
			t.Fatal("unread", err)
		}
		if _, err = s.UpdateProductNotification(ctx, scope, req); !errors.Is(err, ErrConflict) {
			t.Fatal("old retry restored read state", err)
		}
		if _, err = s.UpdateProductHelp(ctx, scope.AccountID, scope.AppID, r.Request.ID, api.ProductHelpUpdate{ExpectedVersion: 2, Status: "resolved", Response: "New response"}); err != nil {
			t.Fatal(err)
		}
		p = notificationTestPage(ctx, t, s, scope, "unread", "")
		if len(p.Items) != 1 || p.Items[0].Revision == n.Revision || p.Items[0].Read {
			t.Fatal("new reply remained read", p)
		}
		if _, err = s.UpdateProductNotification(ctx, scope, notificationTestUpdate(unread, true)); !errors.Is(err, ErrConflict) {
			t.Fatal("old source acknowledged a new reply", err)
		}
		raw, _ := json.Marshal(p)
		for _, private := range []string{"Private owner response", "New response", "message", "context", "unit_price_millicents", "auth_organization_id"} {
			if strings.Contains(string(raw), private) {
				t.Fatal("notification leaked", private)
			}
		}
		after, err := s.GetProductUsage(ctx, scope.AccountID, scope.AppID, scope.TenantID, before.Period)
		if err != nil || !reflect.DeepEqual(after, before) {
			t.Fatal("read metadata changed usage", err)
		}
	})
	t.Run("current permissions filter before counts and pages", func(t *testing.T) {
		f := helpFixture(ctx, t, s)
		scope := ProductNotificationScope{f.scope}
		scope.Role = "operator"
		a := f.admission
		a.IdempotencyKey = uuid.NewString()
		op, _, err := s.AdmitOperation(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		op.State = api.OperationSucceeded
		op.UpdatedAt = time.Now().UTC()
		settle(op)
		notificationTestReply(ctx, t, s, scope, "workspace", "", "")
		feature := notificationTestReply(ctx, t, s, scope, "feature", "export", "")
		notificationTestReply(ctx, t, s, scope, "run", "", op.ID)
		for _, tc := range []struct {
			role  string
			count int
		}{{"operator", 4}, {"viewer", 3}, {"billing", 2}, {"team", 1}, {"manager", 1}} {
			visible := scope
			visible.Role = tc.role
			p := notificationTestPage(ctx, t, s, visible, "all", "")
			if len(p.Items) != tc.count || p.UnreadCount != int64(tc.count) {
				t.Fatalf("%s: %+v", tc.role, p)
			}
		}
		p := notificationTestPage(ctx, t, s, scope, "all", "")
		var hidden api.ProductNotification
		for _, n := range p.Items {
			if n.ReferenceID == feature.Request.ID {
				hidden = n
			}
		}
		viewer := scope
		viewer.Role = "viewer"
		if _, err = s.UpdateProductNotification(ctx, viewer, notificationTestUpdate(hidden, true)); !errors.Is(err, ErrNotFound) {
			t.Fatal("unauthorized ack", err)
		}
		f.spec.Access.Roles["viewer"] = []api.ProductPermission{}
		if _, err = s.PutProductCatalog(ctx, scope.AccountID, scope.AppID, f.spec); err != nil {
			t.Fatal(err)
		}
		if _, err = s.ListProductNotifications(ctx, viewer, "all", ""); !errors.Is(err, ErrProductAccess) {
			t.Fatal("revoked role", err)
		}
	})
	t.Run("pagination, unread filters and cursor scopes", func(t *testing.T) {
		f := helpFixture(ctx, t, s)
		scope := ProductNotificationScope{f.scope}
		scope.Role = "operator"
		for range api.ProductControlNotificationPageSize + 1 {
			notificationTestReply(ctx, t, s, scope, "workspace", "", "")
		}
		p := notificationTestPage(ctx, t, s, scope, "all", "")
		if len(p.Items) != 20 || p.NextCursor == "" || p.UnreadCount != 21 {
			t.Fatal("page", p)
		}
		older := notificationTestPage(ctx, t, s, scope, "all", p.NextCursor)
		if len(older.Items) != 1 || older.NextCursor != "" || older.UnreadCount != 21 {
			t.Fatal("lookahead", older)
		}
		if _, err := s.ListProductNotifications(ctx, scope, "unread", p.NextCursor); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal("cursor crossed filters", err)
		}
		if _, err := s.UpdateProductNotification(ctx, scope, notificationTestUpdate(p.Items[0], true)); err != nil {
			t.Fatal(err)
		}
		unread := notificationTestPage(ctx, t, s, scope, "unread", "")
		if len(unread.Items) != 20 || unread.UnreadCount != 20 || unread.NextCursor != "" {
			t.Fatal("unread before limit", unread)
		}
		for _, bad := range []string{"bad-cursor", strings.Repeat("x", api.ProductControlOwnerCursorMaxBytes+1)} {
			if _, err := s.ListProductNotifications(ctx, scope, "all", bad); !errors.Is(err, ErrInvalidArgument) {
				t.Fatal("bad cursor", err)
			}
		}
	})
	t.Run("organization remapping isolates help and read state", func(t *testing.T) {
		f := helpFixture(ctx, t, s)
		scope := ProductNotificationScope{f.scope}
		scope.Role = "operator"
		notificationTestReply(ctx, t, s, scope, "workspace", "", "")
		p := notificationTestPage(ctx, t, s, scope, "all", "")
		if _, err := s.UpdateProductNotification(ctx, scope, notificationTestUpdate(p.Items[0], true)); err != nil {
			t.Fatal(err)
		}
		c := f.customer
		c.AuthOrganizationID = uuid.NewString()
		if _, err := s.SetProductCustomer(ctx, scope.AccountID, scope.AppID, c); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ListProductNotifications(ctx, scope, "all", ""); !errors.Is(err, ErrProductAccess) {
			t.Fatal("old organization", err)
		}
		scope.OrganizationID = c.AuthOrganizationID
		p = notificationTestPage(ctx, t, s, scope, "all", "")
		if len(p.Items) != 0 || p.UnreadCount != 0 {
			t.Fatal("inherited help", p)
		}
	})
	t.Run("concurrent acknowledgements converge without work mutations", func(t *testing.T) {
		f := helpFixture(ctx, t, s)
		scope := ProductNotificationScope{f.scope}
		scope.Role = "operator"
		notificationTestReply(ctx, t, s, scope, "workspace", "", "")
		p := notificationTestPage(ctx, t, s, scope, "all", "")
		req := notificationTestUpdate(p.Items[0], true)
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				n, err := s.UpdateProductNotification(ctx, scope, req)
				if err != nil || !n.Read || n.ReadVersion != 1 {
					t.Errorf("concurrent ack: %+v %v", n, err)
				}
			})
		}
		wg.Wait()
		if p = notificationTestPage(ctx, t, s, scope, "unread", ""); len(p.Items) != 0 || p.UnreadCount != 0 {
			t.Fatal("empty page count", p)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := s.UpdateProductNotification(cancelled, scope, notificationTestUpdate(api.ProductNotification{ID: req.ID, Revision: req.Revision, Read: true, ReadVersion: 1}, false)); err == nil {
			t.Fatal("cancelled update accepted")
		}
	})
	t.Run("retained outcomes and window boundaries", func(t *testing.T) {
		for _, state := range []api.OperationState{api.OperationSucceeded, api.OperationFailed, api.OperationCancelled} {
			f := helpFixture(ctx, t, s)
			scope := ProductNotificationScope{f.scope}
			scope.Role = "operator"
			a := f.admission
			a.IdempotencyKey = uuid.NewString()
			op, _, err := s.AdmitOperation(ctx, a)
			if err != nil {
				t.Fatal(err)
			}
			op.State = state
			op.UpdatedAt = time.Now().UTC().Add(-time.Hour)
			settle(op)
			p := notificationTestPage(ctx, t, s, scope, "all", "")
			if len(p.Items) != 1 || p.Items[0].Kind != "work_"+string(state) {
				t.Fatalf("outcome: %+v", p)
			}
		}
		f := helpFixture(ctx, t, s)
		scope := ProductNotificationScope{f.scope}
		scope.Role = "operator"
		a := f.admission
		a.IdempotencyKey = uuid.NewString()
		op, _, err := s.AdmitOperation(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		op.State = api.OperationFailed
		op.UpdatedAt = time.Now().UTC().Add(-api.ProductControlNotificationWindow - time.Hour)
		settle(op)
		if p := notificationTestPage(ctx, t, s, scope, "all", ""); len(p.Items) != 0 || p.UnreadCount != 0 {
			t.Fatal("outside window", p)
		}
	})
}
