package main

import (
	"flag"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
)

func writeAdmissionFlags(flags *flag.FlagSet) *objectnode.WriteAdmissionOptions {
	o := objectnode.DefaultWriteAdmissionOptions()
	flags.BoolVar(&o.Enabled, "write-admission", o.Enabled, "throttle new writes under replication, retention or disk pressure")
	flags.Int64Var(&o.LagHigh, "admit-lag-high", o.LagHigh, "projected records behind the slowest required retention pin that close admission")
	flags.Int64Var(&o.LagLow, "admit-lag-low", o.LagLow, "projected pinned lag at or below which admission can reopen")
	flags.Int64Var(&o.RecordsHigh, "admit-records-high", o.RecordsHigh, "projected retained journal/payload file count that closes admission")
	flags.Int64Var(&o.RecordsLow, "admit-records-low", o.RecordsLow, "projected retained file count at or below which admission can reopen")
	flags.Int64Var(&o.JournalHigh, "admit-journal-high", o.JournalHigh, "projected retained journal bytes that close admission")
	flags.Int64Var(&o.JournalLow, "admit-journal-low", o.JournalLow, "projected retained journal bytes at or below which admission can reopen")
	flags.Int64Var(&o.ReplayHigh, "admit-replay-high", o.ReplayHigh, "projected retained replay bytes that close admission")
	flags.Int64Var(&o.ReplayLow, "admit-replay-low", o.ReplayLow, "projected retained replay bytes at or below which admission can reopen")
	flags.Int64Var(&o.ResumeFreeBytes, "admit-resume-free-bytes", 0, "free bytes after projected reservations needed to reopen; zero uses min-free-bytes plus 512 MiB")
	return &o
}

func resolveWriteAdmission(o *objectnode.WriteAdmissionOptions, minFree int64) error {
	if minFree == 0 {
		minFree = api.ObjectUploadSpoolMinFreeBytes
	}
	if minFree < 0 || minFree > api.MaxObjectStoragePolicyValue-api.ObjectNodeAdmissionFreeRecovery {
		return objectnode.ErrInvalid
	}
	o.MinFreeBytes = minFree
	if o.ResumeFreeBytes == 0 {
		o.ResumeFreeBytes = minFree + api.ObjectNodeAdmissionFreeRecovery
	}
	return o.Validate()
}
