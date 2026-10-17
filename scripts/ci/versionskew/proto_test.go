package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func field(name string, number int32, typ descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(number),
		Type:     typ.Enum(),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		JsonName: proto.String(name),
	}
}

func deprecatedField(f *descriptorpb.FieldDescriptorProto) *descriptorpb.FieldDescriptorProto {
	f.Options = &descriptorpb.FieldOptions{Deprecated: proto.Bool(true)}
	return f
}

const tString = descriptorpb.FieldDescriptorProto_TYPE_STRING

// baseFile is a small vmmd-like service: Create(CreateRequest) returns
// (CreateReply), plus a status enum.
func baseFile() *descriptorpb.FileDescriptorProto {
	return &descriptorpb.FileDescriptorProto{
		Name:    proto.String("onebox/faas/test/v1/test.proto"),
		Package: proto.String("onebox.faas.test.v1"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("CreateRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("app_id", 1, tString),
					field("ram_mb", 2, descriptorpb.FieldDescriptorProto_TYPE_INT32),
				},
			},
			{Name: proto.String("CreateReply"), Field: []*descriptorpb.FieldDescriptorProto{field("id", 1, tString)}},
		},
		EnumType: []*descriptorpb.EnumDescriptorProto{{
			Name: proto.String("State"),
			Value: []*descriptorpb.EnumValueDescriptorProto{
				{Name: proto.String("STATE_UNSPECIFIED"), Number: proto.Int32(0)},
				{Name: proto.String("STATE_RUNNING"), Number: proto.Int32(1)},
			},
		}},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("Vmm"),
			Method: []*descriptorpb.MethodDescriptorProto{
				{Name: proto.String("Create"), InputType: proto.String(".onebox.faas.test.v1.CreateRequest"), OutputType: proto.String(".onebox.faas.test.v1.CreateReply")},
				{Name: proto.String("Destroy"), InputType: proto.String(".onebox.faas.test.v1.CreateRequest"), OutputType: proto.String(".onebox.faas.test.v1.CreateReply")},
			},
		}},
	}
}

func TestCompareProto(t *testing.T) {
	msg := func(f *descriptorpb.FileDescriptorProto, name string) *descriptorpb.DescriptorProto {
		for _, m := range f.MessageType {
			if m.GetName() == name {
				return m
			}
		}
		t.Fatalf("no message %s", name)
		return nil
	}
	tests := []struct {
		name   string
		before func(f *descriptorpb.FileDescriptorProto) // applied to the old release
		after  func(f *descriptorpb.FileDescriptorProto) // applied to head
		want   []string
	}{
		{name: "unchanged"},
		{
			name: "added field, RPC and enum value are compatible",
			after: func(f *descriptorpb.FileDescriptorProto) {
				m := msg(f, "CreateRequest")
				m.Field = append(m.Field, field("region", 3, tString))
				f.Service[0].Method = append(f.Service[0].Method, &descriptorpb.MethodDescriptorProto{Name: proto.String("Pause"), InputType: proto.String(".onebox.faas.test.v1.CreateRequest"), OutputType: proto.String(".onebox.faas.test.v1.CreateReply")})
				f.EnumType[0].Value = append(f.EnumType[0].Value, &descriptorpb.EnumValueDescriptorProto{Name: proto.String("STATE_PARKED"), Number: proto.Int32(2)})
			},
		},
		{
			name:  "RPC removed without deprecation",
			after: func(f *descriptorpb.FileDescriptorProto) { f.Service[0].Method = f.Service[0].Method[:1] },
			want:  []string{"proto:onebox.faas.test.v1.Vmm.Destroy:rpc-removed"},
		},
		{
			name: "RPC removed after deprecation in the previous release",
			before: func(f *descriptorpb.FileDescriptorProto) {
				f.Service[0].Method[1].Options = &descriptorpb.MethodOptions{Deprecated: proto.Bool(true)}
			},
			after: func(f *descriptorpb.FileDescriptorProto) { f.Service[0].Method = f.Service[0].Method[:1] },
		},
		{
			name:  "service removed without deprecation",
			after: func(f *descriptorpb.FileDescriptorProto) { f.Service = nil },
			want:  []string{"proto:onebox.faas.test.v1.Vmm:service-removed"},
		},
		{
			name: "deprecated service removed",
			before: func(f *descriptorpb.FileDescriptorProto) {
				f.Service[0].Options = &descriptorpb.ServiceOptions{Deprecated: proto.Bool(true)}
			},
			after: func(f *descriptorpb.FileDescriptorProto) { f.Service = nil },
		},
		{
			name: "request type changed",
			after: func(f *descriptorpb.FileDescriptorProto) {
				f.Service[0].Method[0].InputType = proto.String(".onebox.faas.test.v1.CreateReply")
			},
			want: []string{"proto:onebox.faas.test.v1.Vmm.Create:rpc-request-changed"},
		},
		{
			name:  "streaming changed",
			after: func(f *descriptorpb.FileDescriptorProto) { f.Service[0].Method[0].ServerStreaming = proto.Bool(true) },
			want:  []string{"proto:onebox.faas.test.v1.Vmm.Create:rpc-streaming-changed"},
		},
		{
			name: "field removed without deprecation",
			after: func(f *descriptorpb.FileDescriptorProto) {
				msg(f, "CreateRequest").Field = msg(f, "CreateRequest").Field[:1]
			},
			want: []string{"proto:onebox.faas.test.v1.CreateRequest#2:field-removed"},
		},
		{
			name:   "deprecated field removed but not reserved",
			before: func(f *descriptorpb.FileDescriptorProto) { deprecatedField(msg(f, "CreateRequest").Field[1]) },
			after: func(f *descriptorpb.FileDescriptorProto) {
				msg(f, "CreateRequest").Field = msg(f, "CreateRequest").Field[:1]
			},
			want: []string{"proto:onebox.faas.test.v1.CreateRequest#2:field-not-reserved"},
		},
		{
			name:   "deprecated field removed and reserved",
			before: func(f *descriptorpb.FileDescriptorProto) { deprecatedField(msg(f, "CreateRequest").Field[1]) },
			after: func(f *descriptorpb.FileDescriptorProto) {
				m := msg(f, "CreateRequest")
				m.Field = m.Field[:1]
				m.ReservedRange = []*descriptorpb.DescriptorProto_ReservedRange{{Start: proto.Int32(2), End: proto.Int32(3)}}
				m.ReservedName = []string{"ram_mb"}
			},
		},
		{
			name: "field renamed",
			after: func(f *descriptorpb.FileDescriptorProto) {
				fd := msg(f, "CreateRequest").Field[1]
				fd.Name, fd.JsonName = proto.String("memory_mb"), proto.String("memoryMb")
			},
			want: []string{"proto:onebox.faas.test.v1.CreateRequest#2:field-renamed"},
		},
		{
			name: "field type changed",
			after: func(f *descriptorpb.FileDescriptorProto) {
				msg(f, "CreateRequest").Field[1].Type = descriptorpb.FieldDescriptorProto_TYPE_INT64.Enum()
			},
			want: []string{"proto:onebox.faas.test.v1.CreateRequest#2:field-type-changed"},
		},
		{
			name: "field became repeated",
			after: func(f *descriptorpb.FileDescriptorProto) {
				msg(f, "CreateRequest").Field[0].Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
			},
			want: []string{"proto:onebox.faas.test.v1.CreateRequest#1:field-cardinality-changed"},
		},
		{
			name: "field moved into a oneof",
			after: func(f *descriptorpb.FileDescriptorProto) {
				m := msg(f, "CreateRequest")
				m.OneofDecl = []*descriptorpb.OneofDescriptorProto{{Name: proto.String("target")}}
				m.Field[0].OneofIndex = proto.Int32(0)
			},
			want: []string{"proto:onebox.faas.test.v1.CreateRequest#1:field-oneof-changed"},
		},
		{
			name: "proto3 optional added is compatible",
			after: func(f *descriptorpb.FileDescriptorProto) {
				m := msg(f, "CreateRequest")
				m.OneofDecl = []*descriptorpb.OneofDescriptorProto{{Name: proto.String("_ram_mb")}}
				m.Field[1].OneofIndex = proto.Int32(0)
				m.Field[1].Proto3Optional = proto.Bool(true)
			},
		},
		{
			name: "reserved number reused",
			before: func(f *descriptorpb.FileDescriptorProto) {
				msg(f, "CreateRequest").ReservedRange = []*descriptorpb.DescriptorProto_ReservedRange{{Start: proto.Int32(7), End: proto.Int32(8)}}
			},
			after: func(f *descriptorpb.FileDescriptorProto) {
				m := msg(f, "CreateRequest")
				m.Field = append(m.Field, field("zone", 7, tString))
			},
			want: []string{"proto:onebox.faas.test.v1.CreateRequest#7:reserved-number-reused"},
		},
		{
			name:  "enum value removed without deprecation",
			after: func(f *descriptorpb.FileDescriptorProto) { f.EnumType[0].Value = f.EnumType[0].Value[:1] },
			want:  []string{"proto:onebox.faas.test.v1.State=1:enum-value-removed"},
		},
		{
			name: "deprecated enum value removed and reserved",
			before: func(f *descriptorpb.FileDescriptorProto) {
				f.EnumType[0].Value[1].Options = &descriptorpb.EnumValueOptions{Deprecated: proto.Bool(true)}
			},
			after: func(f *descriptorpb.FileDescriptorProto) {
				e := f.EnumType[0]
				e.Value = e.Value[:1]
				e.ReservedRange = []*descriptorpb.EnumDescriptorProto_EnumReservedRange{{Start: proto.Int32(1), End: proto.Int32(1)}}
				e.ReservedName = []string{"STATE_RUNNING"}
			},
		},
		{
			name:  "enum value renamed",
			after: func(f *descriptorpb.FileDescriptorProto) { f.EnumType[0].Value[1].Name = proto.String("STATE_UP") },
			want:  []string{"proto:onebox.faas.test.v1.State=1:enum-value-renamed"},
		},
		{
			name: "enum alias keeps the old name",
			after: func(f *descriptorpb.FileDescriptorProto) {
				e := f.EnumType[0]
				e.Options = &descriptorpb.EnumOptions{AllowAlias: proto.Bool(true)}
				e.Value = []*descriptorpb.EnumValueDescriptorProto{e.Value[0], {Name: proto.String("STATE_UP"), Number: proto.Int32(1)}, e.Value[1]}
			},
		},
		{
			name: "nested message field type changed",
			before: func(f *descriptorpb.FileDescriptorProto) {
				msg(f, "CreateRequest").NestedType = []*descriptorpb.DescriptorProto{{Name: proto.String("Limits"), Field: []*descriptorpb.FieldDescriptorProto{field("cpu", 1, tString)}}}
			},
			after: func(f *descriptorpb.FileDescriptorProto) {
				msg(f, "CreateRequest").NestedType = []*descriptorpb.DescriptorProto{{Name: proto.String("Limits"), Field: []*descriptorpb.FieldDescriptorProto{field("cpu", 1, descriptorpb.FieldDescriptorProto_TYPE_DOUBLE)}}}
			},
			want: []string{"proto:onebox.faas.test.v1.CreateRequest.Limits#1:field-type-changed"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old, cur := baseFile(), baseFile()
			if tt.before != nil {
				tt.before(old)
				tt.before(cur)
			}
			if tt.after != nil {
				tt.after(cur)
			}
			got := keys(compareProto(newProtoSchema([]*descriptorpb.FileDescriptorProto{old}), newProtoSchema([]*descriptorpb.FileDescriptorProto{cur})))
			assertKeys(t, got, tt.want)
		})
	}
}

// TestLoadProtoCompiles exercises the protoc path end to end on .proto text.
func TestLoadProtoCompiles(t *testing.T) {
	if _, err := exec.LookPath(envOr("PROTOC", "protoc")); err != nil {
		t.Skip("protoc not on PATH")
	}
	write := func(src string) string {
		repo := t.TempDir()
		path := filepath.Join(repo, "api/proto/onebox/faas/test/v1/test.proto")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return repo
	}
	const header = `syntax = "proto3";
package onebox.faas.test.v1;
import "google/protobuf/timestamp.proto";
`
	oldRepo := write(header + `
message Req { string app_id = 1; google.protobuf.Timestamp at = 2; }
message Rep {}
service Vmm { rpc Create(Req) returns (Rep); }
`)
	newRepo := write(header + `
message Req { string app_id = 1; int64 at = 2; }
message Rep {}
service Vmm { rpc Create(Req) returns (Rep); }
`)
	old, err := loadProto(oldRepo, "api/proto", "")
	if err != nil {
		t.Fatal(err)
	}
	cur, err := loadProto(newRepo, "api/proto", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := old.messages["google.protobuf.Timestamp"]; ok {
		t.Error("imported well-known types must not be indexed")
	}
	assertKeys(t, keys(compareProto(old, cur)), []string{"proto:onebox.faas.test.v1.Req#2:field-type-changed"})
}

func keys(fs []finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Key)
	}
	sort.Strings(out)
	return out
}

func assertKeys(t *testing.T, got, want []string) {
	t.Helper()
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("findings = %q, want %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("findings = %q, want %q", got, want)
		}
	}
}
