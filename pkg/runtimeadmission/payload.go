package runtimeadmission

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// HashBootPayload includes the boot variant and every delivered field. The
// grant is excluded to avoid a self-referential hash. Unknown fields at any
// depth are refused: an older node cannot hash a new control and ignore it.
func HashBootPayload(request *vmmdpb.CreateAdmittedRuntimeRequest) (string, error) {
	if request == nil || request.Boot == nil {
		return "", ErrInvalid
	}
	if err := RejectUnknown(request); err != nil {
		return "", err
	}
	copy := proto.Clone(request).(*vmmdpb.CreateAdmittedRuntimeRequest)
	copy.Binding = nil
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(copy)
	if err != nil {
		return "", ErrInvalid
	}
	hash := sha256.Sum256(append([]byte("gregale.runtime-boot.v1\x00"), raw...))
	return hex.EncodeToString(hash[:]), nil
}

func RejectUnknown(message proto.Message) error {
	if message == nil {
		return ErrInvalid
	}
	return rejectUnknown(message.ProtoReflect())
}

func rejectUnknown(message protoreflect.Message) error {
	if !message.IsValid() || len(message.GetUnknown()) != 0 {
		return ErrInvalid
	}
	var failure error
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.IsMap() && field.MapValue().Kind() == protoreflect.MessageKind {
			value.Map().Range(func(_ protoreflect.MapKey, v protoreflect.Value) bool {
				failure = rejectUnknown(v.Message())
				return failure == nil
			})
		} else if field.IsList() && field.Kind() == protoreflect.MessageKind {
			list := value.List()
			for i := 0; i < list.Len() && failure == nil; i++ {
				failure = rejectUnknown(list.Get(i).Message())
			}
		} else if field.Kind() == protoreflect.MessageKind && !field.IsMap() {
			failure = rejectUnknown(value.Message())
		}
		return failure == nil
	})
	if failure != nil {
		return fmt.Errorf("%w: unsupported nested input", ErrInvalid)
	}
	return nil
}
