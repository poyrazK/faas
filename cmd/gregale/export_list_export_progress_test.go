package main

import (
	"context"
	"io"
)

type ListExportProgressEvent = listExportProgressEvent
type ListExportProgressMode = listExportProgressMode
type ListExportProgressReporter = listExportProgress

const ListProgressRequiresExport = listProgressRequiresExport

var ParseListExportProgress = parseListExportProgress
var NewListExportProgress = newListExportProgress

func ObserveListExportProgress(p *ListExportProgressReporter, progress listStreamProgress, bytes int64) {
	p.observe(progress, bytes)
}
func FinishListExportProgress(p *ListExportProgressReporter, published bool, progress listStreamProgress, bytes int64) {
	p.finish(published, progress, bytes)
}
func ListExportBytes(file *listOutputFile) int64 { return file.bytes }
func StreamListPagesObserved[T any](ctx context.Context, dst io.Writer, cursor string, fetch func(context.Context, string) ([]T, string, error), observe func(listStreamProgress)) (listStreamProgress, error) {
	return streamListPagesObserved(ctx, dst, cursor, fetch, observe)
}
