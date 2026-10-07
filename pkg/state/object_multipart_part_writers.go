package state

import "context"

// Dispatch claims an admitted transfer exactly once before provider IO. Finish
// requires validated synchronous success or qualified rejection without mutation;
// it atomically settles that transfer and its pinned receipt. No timeout proves it.
type ObjectMultipartPartMutationStore interface {
	DispatchObjectMultipartPartMutation(context.Context, ObjectBucket, string, int32, string) (ObjectBucketMutation, error)
	FinishObjectMultipartPartMutation(context.Context, ObjectBucketMutation) error
}

type multipartPartWriterKey struct {
	upload string
	part   int32
	token  string
}
type multipartPartWriter struct {
	receipt             ObjectBucketMutation
	dispatched, settled bool
}
