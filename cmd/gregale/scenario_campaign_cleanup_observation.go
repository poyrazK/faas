package main

import (
 "errors"
 "os"
 "path/filepath"
 "regexp"
 "sort"
 "strings"
 "time"
)

// Version one records remain readable but lack the binding needed for linkage.
type testCampaignCleanupObservation struct{
 SchemaVersion int `json:"schema_version"`
 PlanSHA256 string `json:"plan_sha256"`
 CheckpointSHA256 string `json:"checkpoint_sha256,omitempty"`
 BatchID string `json:"batch_id,omitempty"`
 Side string `json:"side,omitempty"`
 Revision string `json:"revision,omitempty"`
 Evidence string `json:"evidence,omitempty"`
 ReportSHA256 string `json:"report_sha256,omitempty"`
 JournalSHA256 string `json:"journal_sha256,omitempty"`
 JournalAfterSHA256 string `json:"journal_after_sha256,omitempty"`
 TargetsSHA256 string `json:"targets_sha256,omitempty"`
 ObservedAt time.Time `json:"observed_at,omitempty"`
 OperationComplete bool `json:"operation_complete,omitempty"`
 Executed bool `json:"executed"`
 Actions []testCampaignCleanupAction `json:"actions"`
}
func testCampaignCleanupTargetsSHA256(targets map[string]testCampaignCleanupAction)string{
 identities:=make(map[string]testCampaignCleanupAction,len(targets))
 for key,target:=range targets{target.Status="";identities[key]=target}
 return testCampaignJSONHash(identities)
}
type testCampaignCleanupObservationPaths []string
func (p *testCampaignCleanupObservationPaths) String()string{return strings.Join(*p,",")}
func (p *testCampaignCleanupObservationPaths) Set(value string)error{
 if value==""||len(*p)>=256{return errors.New("supply at most 256 nonempty cleanup observation paths")}
 *p=append(*p,value);return nil
}
func collectTestCampaignCleanupObservationPaths(directory string,explicit []string)([]string,error){
 entries,err:=os.ReadDir(directory)
 if err!=nil{return nil,err}
 paths:=append([]string{},explicit...)
 pattern:=regexp.MustCompile(`^cleanup-[0-9]+\.json$`)
 for _,entry:=range entries{if pattern.MatchString(entry.Name()){paths=append(paths,filepath.Join(directory,entry.Name()))}}
 seen:=map[string]bool{}
 result:=[]string{}
 for _,name:=range paths{
  absolute,err:=filepath.Abs(name)
  if err!=nil{return nil,err}
  if !seen[absolute]{seen[absolute]=true;result=append(result,absolute)}
 }
 if len(result)>256{return nil,errors.New("completion verification supports at most 256 cleanup observations")}
 sort.Strings(result)
 return result,nil
}
