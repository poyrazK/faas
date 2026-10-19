package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// protoSchema indexes the services, messages and enums of the repository's
// own .proto files by fully qualified name (imports such as
// google/protobuf/timestamp.proto are compiled but not indexed).
type protoSchema struct {
	services map[string]*descriptorpb.ServiceDescriptorProto
	messages map[string]*descriptorpb.DescriptorProto
	enums    map[string]*descriptorpb.EnumDescriptorProto
}

// loadProto compiles every .proto under root at ref ("" = working tree) with
// protoc, the same compiler `make proto` uses.
func loadProto(repo, root, ref string) (*protoSchema, error) {
	files, err := readTree(repo, ref, root, func(name string) bool { return strings.HasSuffix(name, ".proto") })
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return newProtoSchema(nil), nil
	}
	tmp, err := os.MkdirTemp("", "versionskew-proto-")
	if err != nil {
		return nil, fmt.Errorf("temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	src := filepath.Join(tmp, "src")
	var names []string
	for name, data := range files {
		rel := strings.TrimPrefix(name, strings.TrimSuffix(root, "/")+"/")
		dst := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, fmt.Errorf("stage %s: %w", name, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return nil, fmt.Errorf("stage %s: %w", name, err)
		}
		names = append(names, rel)
	}
	sort.Strings(names)
	out := filepath.Join(tmp, "descriptors.pb")
	args := append([]string{"--proto_path=" + src, "--include_imports", "--descriptor_set_out=" + out}, names...)
	cmd := exec.Command(envOr("PROTOC", "protoc"), args...)
	if msg, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("protoc at %s: %w: %s", refName(ref), err, strings.TrimSpace(string(msg)))
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("read descriptor set: %w", err)
	}
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("decode descriptor set: %w", err)
	}
	own := map[string]bool{}
	for _, n := range names {
		own[n] = true
	}
	var mine []*descriptorpb.FileDescriptorProto
	for _, f := range set.GetFile() {
		if own[f.GetName()] {
			mine = append(mine, f)
		}
	}
	return newProtoSchema(mine), nil
}

func newProtoSchema(files []*descriptorpb.FileDescriptorProto) *protoSchema {
	s := &protoSchema{
		services: map[string]*descriptorpb.ServiceDescriptorProto{},
		messages: map[string]*descriptorpb.DescriptorProto{},
		enums:    map[string]*descriptorpb.EnumDescriptorProto{},
	}
	for _, f := range files {
		prefix := ""
		if f.GetPackage() != "" {
			prefix = f.GetPackage() + "."
		}
		for _, svc := range f.GetService() {
			s.services[prefix+svc.GetName()] = svc
		}
		for _, e := range f.GetEnumType() {
			s.enums[prefix+e.GetName()] = e
		}
		for _, m := range f.GetMessageType() {
			s.addMessage(prefix+m.GetName(), m)
		}
	}
	return s
}

func (s *protoSchema) addMessage(name string, m *descriptorpb.DescriptorProto) {
	s.messages[name] = m
	for _, e := range m.GetEnumType() {
		s.enums[name+"."+e.GetName()] = e
	}
	for _, nested := range m.GetNestedType() {
		s.addMessage(name+"."+nested.GetName(), nested)
	}
}

const protoRemedy = "Deprecate it in one release (option deprecated = true; stop using it), remove it in the next."

// compareProto reports what an N-1 peer built from old would trip over when
// talking to a peer built from cur, in either direction.
func compareProto(old, cur *protoSchema) []finding {
	var out []finding
	add := func(subject, rule, format string, args ...any) {
		out = append(out, finding{Key: "proto:" + subject + ":" + rule, Message: fmt.Sprintf(format, args...)})
	}

	for name, svc := range old.services {
		next, ok := cur.services[name]
		if !ok {
			if !svc.GetOptions().GetDeprecated() {
				add(name, "service-removed", "service %s was removed without being deprecated in the previous release. %s", name, protoRemedy)
			}
			continue
		}
		methods := map[string]*descriptorpb.MethodDescriptorProto{}
		for _, m := range next.GetMethod() {
			methods[m.GetName()] = m
		}
		for _, m := range svc.GetMethod() {
			rpc := name + "." + m.GetName()
			n, ok := methods[m.GetName()]
			switch {
			case !ok:
				if !m.GetOptions().GetDeprecated() && !svc.GetOptions().GetDeprecated() {
					add(rpc, "rpc-removed", "RPC %s was removed without being deprecated in the previous release; an N-1 caller gets Unimplemented. %s", rpc, protoRemedy)
				}
			case n.GetInputType() != m.GetInputType():
				add(rpc, "rpc-request-changed", "RPC %s request type changed from %s to %s; add a new RPC instead.", rpc, m.GetInputType(), n.GetInputType())
			case n.GetOutputType() != m.GetOutputType():
				add(rpc, "rpc-response-changed", "RPC %s response type changed from %s to %s; add a new RPC instead.", rpc, m.GetOutputType(), n.GetOutputType())
			case n.GetClientStreaming() != m.GetClientStreaming() || n.GetServerStreaming() != m.GetServerStreaming():
				add(rpc, "rpc-streaming-changed", "RPC %s changed its streaming mode; add a new RPC instead.", rpc)
			}
		}
	}

	// A removed message is caught where it was used: as an RPC type or as a
	// field type, both of which are compared above and below.
	for name, msg := range old.messages {
		next, ok := cur.messages[name]
		if !ok {
			continue
		}
		out = append(out, compareFields(name, msg, next)...)
	}

	for name, enum := range old.enums {
		next, ok := cur.enums[name]
		if !ok {
			continue
		}
		out = append(out, compareEnum(name, enum, next)...)
	}
	return out
}

func compareFields(msgName string, old, cur *descriptorpb.DescriptorProto) []finding {
	var out []finding
	add := func(f *descriptorpb.FieldDescriptorProto, rule, format string, args ...any) {
		subject := fmt.Sprintf("%s#%d", msgName, f.GetNumber())
		out = append(out, finding{Key: "proto:" + subject + ":" + rule, Message: fmt.Sprintf(format, args...)})
	}
	fields := map[int32]*descriptorpb.FieldDescriptorProto{}
	for _, f := range cur.GetField() {
		fields[f.GetNumber()] = f
	}
	for _, f := range old.GetField() {
		label := fmt.Sprintf("%s.%s (field %d)", msgName, f.GetName(), f.GetNumber())
		n, ok := fields[f.GetNumber()]
		if !ok {
			switch {
			case !f.GetOptions().GetDeprecated():
				add(f, "field-removed", "%s was removed without being deprecated in the previous release; an N-1 reader silently gets its zero value. %s Then reserve its number and name.", label, protoRemedy)
			case !reservesNumber(cur, f.GetNumber()) || !reservesName(cur.GetReservedName(), f.GetName()):
				add(f, "field-not-reserved", "%s was removed; reserve its number and name (reserved %d; reserved %q;) so neither is reused with another meaning.", label, f.GetNumber(), f.GetName())
			}
			continue
		}
		switch {
		case n.GetName() != f.GetName():
			add(f, "field-renamed", "%s was renamed to %q; the JSON encoding and generated accessors change. Add a new field instead.", label, n.GetName())
		case n.GetJsonName() != f.GetJsonName():
			add(f, "field-json-name-changed", "%s changed json_name from %q to %q.", label, f.GetJsonName(), n.GetJsonName())
		case n.GetType() != f.GetType() || n.GetTypeName() != f.GetTypeName():
			add(f, "field-type-changed", "%s changed type from %s to %s; peers decode it differently. Add a new field instead.", label, fieldType(f), fieldType(n))
		case (n.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED) != (f.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED):
			add(f, "field-cardinality-changed", "%s changed between singular and repeated. Add a new field instead.", label)
		case realOneof(old, f) != realOneof(cur, n):
			add(f, "field-oneof-changed", "%s moved from oneof %q to %q; setting another member now clears it for one peer but not the other.", label, realOneof(old, f), realOneof(cur, n))
		}
	}
	for _, n := range cur.GetField() {
		if _, existed := fieldByNumber(old, n.GetNumber()); !existed && reservesNumber(old, n.GetNumber()) {
			add(n, "reserved-number-reused", "%s.%s reuses field number %d, which the previous release reserved.", msgName, n.GetName(), n.GetNumber())
		}
	}
	return out
}

func compareEnum(enumName string, old, cur *descriptorpb.EnumDescriptorProto) []finding {
	var out []finding
	values := map[int32]*descriptorpb.EnumValueDescriptorProto{}
	for _, v := range cur.GetValue() {
		values[v.GetNumber()] = v
	}
	for _, v := range old.GetValue() {
		key := fmt.Sprintf("proto:%s=%d:", enumName, v.GetNumber())
		label := fmt.Sprintf("%s.%s (= %d)", enumName, v.GetName(), v.GetNumber())
		n, ok := values[v.GetNumber()]
		switch {
		case !ok && !v.GetOptions().GetDeprecated():
			out = append(out, finding{Key: key + "enum-value-removed", Message: fmt.Sprintf("%s was removed without being deprecated in the previous release. %s Then reserve its number and name.", label, protoRemedy)})
		case !ok && (!enumReservesNumber(cur, v.GetNumber()) || !reservesName(cur.GetReservedName(), v.GetName())):
			out = append(out, finding{Key: key + "enum-value-not-reserved", Message: fmt.Sprintf("%s was removed; reserve its number and name so neither is reused.", label)})
		case ok && n.GetName() != v.GetName():
			// Two names for one number (allow_alias) keep the old name valid.
			if !enumHasName(cur, v.GetName(), v.GetNumber()) {
				out = append(out, finding{Key: key + "enum-value-renamed", Message: fmt.Sprintf("%s was renamed to %q; JSON peers send the name.", label, n.GetName())})
			}
		}
	}
	return out
}

func fieldByNumber(m *descriptorpb.DescriptorProto, number int32) (*descriptorpb.FieldDescriptorProto, bool) {
	for _, f := range m.GetField() {
		if f.GetNumber() == number {
			return f, true
		}
	}
	return nil, false
}

// reservesNumber reports whether m reserves number (message reserved ranges
// are end-exclusive).
func reservesNumber(m *descriptorpb.DescriptorProto, number int32) bool {
	for _, r := range m.GetReservedRange() {
		if number >= r.GetStart() && number < r.GetEnd() {
			return true
		}
	}
	return false
}

// enumReservesNumber: enum reserved ranges are end-inclusive.
func enumReservesNumber(e *descriptorpb.EnumDescriptorProto, number int32) bool {
	for _, r := range e.GetReservedRange() {
		if number >= r.GetStart() && number <= r.GetEnd() {
			return true
		}
	}
	return false
}

func reservesName(reserved []string, name string) bool {
	for _, r := range reserved {
		if r == name {
			return true
		}
	}
	return false
}

func enumHasName(e *descriptorpb.EnumDescriptorProto, name string, number int32) bool {
	for _, v := range e.GetValue() {
		if v.GetName() == name && v.GetNumber() == number {
			return true
		}
	}
	return false
}

// realOneof names the oneof a field belongs to, ignoring the synthetic oneof
// protoc generates for a proto3 `optional` field: adding or dropping
// `optional` changes presence tracking, not the wire format.
func realOneof(m *descriptorpb.DescriptorProto, f *descriptorpb.FieldDescriptorProto) string {
	if f.OneofIndex == nil || f.GetProto3Optional() {
		return ""
	}
	decls := m.GetOneofDecl()
	if int(f.GetOneofIndex()) >= len(decls) {
		return ""
	}
	return decls[f.GetOneofIndex()].GetName()
}

func fieldType(f *descriptorpb.FieldDescriptorProto) string {
	if f.GetTypeName() != "" {
		return strings.TrimPrefix(f.GetTypeName(), ".")
	}
	return strings.ToLower(strings.TrimPrefix(f.GetType().String(), "TYPE_"))
}

func refName(ref string) string {
	if ref == "" {
		return "working tree"
	}
	return ref
}
