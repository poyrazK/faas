package datapipelines

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func readersView(p datapipeline.Pipeline) dashboard.DataPipelineReaders {
	v := dashboard.DataPipelineReaders{PipelineID: p.ID, Name: p.Name, Revision: p.Revision, Current: p.ReadSubjects, CurrentInput: strings.Join(p.ReadSubjects, "\n"), Tables: p.Tables, CanRequest: datapipeline.CanChangeReaders(p)}
	c := p.ReaderChange
	if c == nil {
		return v
	}
	v.ID, v.State = c.ID, c.State
	v.CanDiscard = c.State == "preview" && p.DesiredState != "deleted"
	v.CanApprove = v.CanDiscard && p.State != "resync_required" && c.PreviewRevision == p.Revision && c.Generation == p.Generation && slices.Equal(c.Before, p.ReadSubjects) && slices.Equal(c.Tables, p.Tables)
	if c.State == "preview" {
		v.Tables = c.Tables
		for _, subject := range c.After {
			if !slices.Contains(c.Before, subject) {
				v.Added = append(v.Added, subject)
			}
		}
		for _, subject := range c.Before {
			if !slices.Contains(c.After, subject) {
				v.Removed = append(v.Removed, subject)
			}
		}
	}
	return v
}

func (h *Handler) manageDashboardReaders(r *http.Request, account string) (string, error) {
	revision, err := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	if err != nil {
		return "", datapipeline.ErrInvalid
	}
	action := strings.TrimPrefix(r.FormValue("action"), "readers_")
	if action == "approve" && (r.FormValue("confirm_readers") != "yes" || r.FormValue("reader_change_id") == "") {
		return "", datapipeline.ErrInvalid
	}
	var subjects []string
	if action == "preview" {
		subjects = strings.Split(strings.ReplaceAll(r.FormValue("readers"), "\r\n", "\n"), "\n")
	}
	err = h.Service.Readers(r.Context(), account, r.FormValue("pipeline_id"), revision, action, r.FormValue("reader_change_id"), subjects)
	return "", err
}
