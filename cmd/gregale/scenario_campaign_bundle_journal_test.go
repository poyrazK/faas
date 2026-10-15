package main

import (
 "bytes"
 "context"
 "encoding/json"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func exportPortableJournalFixture(t *testing.T,uncertain bool)(campaignStatusTestFiles,testCampaignBundle,string,string){
 t.Helper()
 f:=newCampaignStatusTestFiles(t)
 batch:=f.plan.Batches[1]
 f.checkpoint.Batches[batch.ID]=testCampaignCheckpointBatch{Status:"running"}
 if err:=writeTestCampaignJSON(filepath.Join(f.directory,"checkpoint.json"),f.checkpoint);err!=nil{t.Fatal(err)}
 directory,err:=prepareTestCampaignJournal(f.directory,batch)
 if err!=nil{t.Fatal(err)}
 runID:=strings.Repeat("b",32)
 writer,err:=newTestCampaignRunJournal(directory,batch.Scenario,batch.Profile,runID,f.scenarios[batch.Scenario])
 if err!=nil{t.Fatal(err)}
 project:=f.scenarios[batch.Scenario].Project
 if err:=writer.update(project,"","creating");err!=nil{t.Fatal(err)}
 if !uncertain{if err:=writer.update(project,"journal-app","provisioned");err!=nil{t.Fatal(err)}}
 output:=filepath.Join(f.root,"portable-journal.json")
 if code:=cmdTestCampaignBundleExport([]string{"--plan",f.planPath,"--policy",f.policyPath,"--manifest",f.manifestPath,
  "--directory",f.directory,"--output",output});code!=0{t.Fatalf("journal export failed: %d",code)}
 bundle,err:=readTestCampaignBundle(output)
 if err!=nil{t.Fatal(err)}
 return f,bundle,batch.ID,runID
}

func TestPortableJournalRecoveryWithoutSourceMachine(t *testing.T){
 // adr: 984 — portable snapshots retain identities but require recipient ownership.
 f,bundle,batchID,runID:=exportPortableJournalFixture(t,false)
 claims:=filepath.Join(f.root,"claims")
 if err:=os.Mkdir(claims,0o700);err!=nil{t.Fatal(err)}
 imported,err:=importTestCampaignBundle(bundle,filepath.Join(f.root,"recipient"),true,claims)
 if err!=nil{t.Fatal(err)}
 if err:=os.Rename(f.directory,f.directory+"-unavailable");err!=nil{t.Fatal(err)}
 resolver,err:=loadTestCampaignBundleResolver(filepath.Join(imported.Root,"bundle-index.json"))
 if err!=nil{t.Fatal(err)}
 if err:=resolver.requireActive();err!=nil{t.Fatal(err)}
 work:=filepath.Join(imported.Root,"work")
 batch:=f.plan.Batches[1]
 scenario:=f.scenarios[batch.Scenario]
 targets,hash,err:=readTestCampaignJournalTargets(work,testCampaignJSONHash(f.plan),f.checkpoint,batch,scenario)
 if err!=nil||hash==""||targets[scenario.Project+"/"+runID].AppID!="journal-app"{t.Fatalf("portable identity missing: %v",err)}
 if _,err:=os.Stat(filepath.Join(testCampaignJournalDirectory(work,batchID),"inventory.json"));!os.IsNotExist(err){
  t.Fatal("immutable inventory was copied into mutable work snapshots")
 }
 before,err:=os.ReadFile(filepath.Join(imported.Root,"artifacts","nodes","0","batch-"+batchID+".journal",runID+".json"))
 if err!=nil{t.Fatal(err)}
 if err:=recordTestCampaignJournalCleanup(work,batchID,[]testCampaignCleanupAction{
  {Project:scenario.Project,RunID:runID,AppID:"journal-app",Status:"deleted"},
 });err!=nil{t.Fatal(err)}
 after,err:=os.ReadFile(filepath.Join(imported.Root,"artifacts","nodes","0","batch-"+batchID+".journal",runID+".json"))
 if err!=nil||!bytes.Equal(before,after){t.Fatal("cleanup mutated immutable source evidence")}
 result,recovered:=inspectTestCampaignRecoveryReport(context.Background(),filepath.Join(work,"batch-"+batchID+".json"),
  batch,scenario,f.checkpoint,f.checkpoint.Batches[batchID],nil)
 if result.Status!="blocked"||result.AdoptedRuns!=0||len(recovered)!=0{t.Fatal("portable cleanup granted execution coverage")}
 output:=filepath.Join(f.root,"next-handoff.json")
 if code:=cmdTestCampaignBundleExport([]string{"--plan",filepath.Join(imported.Root,"artifacts","plan.json"),
  "--policy",filepath.Join(imported.Root,"artifacts","policy.yaml"),"--manifest",f.manifestPath,
  "--directory",work,"--handoff",resolver.IndexPath,"--output",output});code!=0{t.Fatalf("journal re-export failed: %d",code)}
 if err:=resolver.requireActive();err==nil{t.Fatal("re-export retained old recipient authority")}
 next,err:=readTestCampaignBundle(output)
 if err!=nil{t.Fatal(err)}
 found:=false
 for _,file:=range next.Files{
  if file.Path=="nodes/0/batch-"+batchID+".journal/"+runID+".json"{
   var journal testCampaignRunJournal
   if err:=json.Unmarshal(file.Data,&journal);err!=nil{t.Fatal(err)}
   found=journal.Workloads[scenario.Project].State=="deleted"
  }
 }
 if !found{t.Fatal("cleanup progress was lost on the next handoff")}
}

func TestPortableJournalPreservesUncertainIntent(t *testing.T){
 // adr: 984 — uncertainty travels losslessly and never becomes guessed deletion.
 f,bundle,batchID,_:=exportPortableJournalFixture(t,true)
 index,err:=importTestCampaignBundle(bundle,filepath.Join(f.root,"inspection"),false,"")
 if err!=nil{t.Fatal(err)}
 resolver,err:=loadTestCampaignBundleResolver(filepath.Join(index.Root,"bundle-index.json"))
 if err!=nil{t.Fatal(err)}
 if err:=resolver.requireActive();err==nil{t.Fatal("inspection-only journal import gained cleanup authority")}
 batch:=f.plan.Batches[1]
 if _,_,err:=readTestCampaignJournalTargets(filepath.Join(index.Root,"work"),testCampaignJSONHash(f.plan),f.checkpoint,
  batch,f.scenarios[batch.Scenario]);err==nil{t.Fatal("uncertain provisioning intent became cleanup authorization")}
 if f.checkpoint.Batches[batchID].Status!="running"{t.Fatal("import rewrote source checkpoint status")}
}

// Refresh checksums as a sender could; semantic validation must still reject
// internally inconsistent evidence after outer checksums are recomputed.
func rehashPortableJournalBundle(t *testing.T,bundle *testCampaignBundle){
 t.Helper()
 hashes:=map[string]string{}
 for i:=range bundle.Files{file:=&bundle.Files[i];file.SHA256=testCampaignBundleHash(file.Data);hashes[file.Path]=file.SHA256}
 for i:=range bundle.Files{
  file:=&bundle.Files[i]
  directory,name,ok:=testCampaignPortableJournalPath(file.Path)
  if !ok||name!="inventory.json"{continue}
  var inventory testCampaignPortableJournalInventory
  if err:=json.Unmarshal(file.Data,&inventory);err!=nil{t.Fatal(err)}
  for j:=range inventory.Files{
   if hash:=hashes[directory+"/"+inventory.Files[j].Path];hash!=""{inventory.Files[j].SHA256=hash}
  }
  body,err:=json.Marshal(inventory)
  if err!=nil{t.Fatal(err)}
  file.Data=body;file.SHA256=testCampaignBundleHash(body)
 }
 bundle.ID=testCampaignBundleID(*bundle)
}

func TestPortableJournalRejectsMissingSnapshotAndChangedBinding(t *testing.T){
 // adr: 984 — exact snapshot inventories and checkpoint bindings precede materialization.
 _,bundle,batchID,runID:=exportPortableJournalFixture(t,false)
 prefix:="nodes/0/batch-"+batchID+".journal/"
 changed:=bundle
 changed.Files=append([]testCampaignBundleFile{},bundle.Files...)
 for i,file:=range changed.Files{if file.Path==prefix+runID+".json"{changed.Files=append(changed.Files[:i],changed.Files[i+1:]...);break}}
 rehashPortableJournalBundle(t,&changed)
 if err:=validateTestCampaignBundle(changed);err==nil{t.Fatal("omitted journal snapshot accepted")}
 changed=bundle;changed.Files=append([]testCampaignBundleFile{},bundle.Files...)
 for i,file:=range changed.Files{
  if file.Path!=prefix+runID+".json"{continue}
  var journal testCampaignRunJournal
  if err:=json.Unmarshal(file.Data,&journal);err!=nil{t.Fatal(err)}
  journal.Binding.Revision=strings.Repeat("c",40)
  changed.Files[i].Data,_=json.Marshal(journal)
 }
 rehashPortableJournalBundle(t,&changed)
 if err:=validateTestCampaignBundle(changed);err==nil{t.Fatal("cross-revision journal accepted")}
 if testCampaignBundlePathAllowed(prefix+"../outside.json"){t.Fatal("journal traversal accepted")}
}
