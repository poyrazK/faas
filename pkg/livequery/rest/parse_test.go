package rest

import (
	"errors"
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	req, err := Parse(`messages?select=id,text:body,data->a->>b,n::text,author:users!author_id(name,team:teams(*)),comments!inner(id)&channel_id=eq.1&or=(kind.in.(a,"b,c"),and(score.gt.5,flag.is.true),body.not.ilike.*x*)&order=id.desc.nullslast,body&limit=50&offset=10`)
	if err != nil {
		t.Fatal(err)
	}
	if req.Relation != "messages" || req.Limit != 50 || req.Offset != 10 || len(req.Select) != 6 || len(req.Filter) != 2 || len(req.Order) != 2 {
		t.Fatalf("request %+v", req)
	}
	if it := req.Select[1]; it.Alias != "text" || it.Column != "body" {
		t.Fatalf("alias %+v", it)
	}
	if it := req.Select[2]; it.Column != "data" || !reflect.DeepEqual(it.Path, []PathStep{{Key: "a"}, {Key: "b", Text: true}}) {
		t.Fatalf("path %+v", it)
	}
	if it := req.Select[3]; it.Cast != "text" || it.Column != "n" {
		t.Fatalf("cast %+v", it)
	}
	if e := req.Select[4].Embed; e == nil || e.Relation != "users" || e.Hint != "author_id" || req.Select[4].Alias != "author" || len(e.Select) != 2 || e.Select[1].Embed == nil {
		t.Fatalf("embed %+v", req.Select[4])
	}
	if e := req.Select[5].Embed; e == nil || !e.Inner {
		t.Fatalf("inner embed %+v", req.Select[5])
	}
	var or *Cond
	for _, f := range req.Filter {
		if f.Op == "or" {
			or = f
		}
	}
	if or == nil || len(or.Children) != 3 || !reflect.DeepEqual(or.Children[0].List, []string{"a", "b,c"}) ||
		or.Children[1].Op != "and" || !or.Children[2].Not || or.Children[2].Op != "ilike" {
		t.Fatalf("or %+v", or)
	}
	if o := req.Order[0]; !o.Desc || o.NullsFirst == nil || *o.NullsFirst {
		t.Fatalf("order %+v", o)
	}

	for _, bad := range []string{
		`messages;drop?select=id`,
		`messages?select=id;drop`,
		`messages?select=a"b`,
		`messages?author.name=eq.x`,
		`messages?id=xx.1`,
		`messages?id=is.maybe`,
		`messages?id=in.(1,2`,
		`messages?limit=-1`,
		`messages?order=id.sideways`,
		`messages?select=data->'x'`,
		`messages?columns=id`,
		`messages?id=eq.1&id=eq.2`,
	} {
		if _, err := Parse(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", bad, err)
		}
	}
}
