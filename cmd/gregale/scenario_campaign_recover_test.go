package main

import (
 "bytes"
 "context"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestRecoveryAssessmentCLIIsReadOnly(t *testing.T){
 // adr: 984 — assessment cannot acquire a lock, run fixtures or rewrite recovery evidence.
 f:=newCampaignStatusTestFiles(t)
 checkpointPath:=filepath.Join(f.directory,"checkpoint.json")
 before,err:=os.ReadFile(checkpointPath)
 if err!=nil{t.Fatal(err)}
 entriesBefore,err:=os.ReadDir(f.directory)
 if err!=nil{t.Fatal(err)}
 output:=filepath.Join(f.root,"assessment.json")
 if code:=cmdTestCampaignRecover([]string{"--inspect","--plan",f.planPath,"--policy",f.policyPath,"--manifest",f.manifestPath,
  "--directory",f.directory,"--json",output});code!=0{t.Fatalf("assessment returned %d",code)}
 var result testCampaignRecoveryAssessment
 if err:=readTestCampaignJSON(output,&result);err!=nil{t.Fatal(err)}
 if !result.ReadOnly||!result.Informational||!result.SnapshotStable||result.LiveChecked{t.Fatal("offline assessment flags are incorrect")}
 after,err:=os.ReadFile(checkpointPath)
 if err!=nil||!bytes.Equal(before,after){t.Fatal("checkpoint was changed")}
 entriesAfter,err:=os.ReadDir(f.directory)
 if err!=nil||len(entriesBefore)!=len(entriesAfter){t.Fatal("assessment wrote campaign files")}
 if _,err:=os.Stat(f.directory+"-recovery");!os.IsNotExist(err){t.Fatal("suggested reconciliation output was created")}
 if code:=cmdTestCampaignRecover([]string{"--plan",f.planPath,"--policy",f.policyPath,"--directory",f.directory});code==0{
  t.Fatal("missing explicit inspection mode accepted")
 }
}

func recoveryAssessmentJournalFixture(t *testing.T)(testCampaignPolicyPlan,testCampaignCheckpoint,map[string]testScenario,testCampaignRecoveryAssessmentOptions){
 t.Helper()
 directory,plan,cp,batch,scenario,journal:=campaignJournalTestFixture(t)
 writer,err:=newTestCampaignRunJournal(journal,batch.Scenario,batch.Profile,strings.Repeat("b",32),scenario)
 if err!=nil{t.Fatal(err)}
 if err:=writer.update(scenario.Project,"","creating");err!=nil{t.Fatal(err)}
 if err:=writer.update(scenario.Project,"recorded","provisioned");err!=nil{t.Fatal(err)}
 options:=testCampaignRecoveryAssessmentOptions{Plan:"saved plan's file.json",Policy:"policy.yaml",Manifest:"manifest.yaml",
  Directory:directory,OutputDirectory:directory+"-recovery"}
 return plan,cp,map[string]testScenario{batch.Scenario:scenario},options
}

func assessmentHasExecute(result testCampaignRecoveryAssessment)bool{
 for _,batch:=range result.Batches{for _,command:=range batch.Commands{for _,arg:=range command.Argv{if arg=="--execute"{return true}}}}
 return false
}

func TestRecoveryAssessmentRequiresLiveMatchingIdentityForDeletionAdvice(t *testing.T){
 // adr: 984 — recorded identities alone cannot become a verified deletion recommendation.
 plan,cp,scenarios,options:=recoveryAssessmentJournalFixture(t)
 status:=testCampaignStatusReport{SnapshotStable:true}
 offline:=buildTestCampaignRecoveryAssessment(context.Background(),plan,cp,status,scenarios,options,nil,nil)
 if assessmentHasExecute(offline)||len(offline.Batches)!=1||offline.Batches[0].Evidence!="journal"||
  offline.Batches[0].Resources[0].Live!="not_checked"{t.Fatal("offline identity became live cleanup authorization")}
 fake:=&campaignCleanupIdentityFake{campaignCleanupFake:campaignCleanupFake{app:"replacement"}}
 changed:=buildTestCampaignRecoveryAssessment(context.Background(),plan,cp,status,scenarios,options,nil,fake)
 if assessmentHasExecute(changed)||changed.Batches[0].Resources[0].Live!="identity_changed"||fake.deletes!=0{
  t.Fatal("assessment mutated a replacement app or recommended deletion")
 }
 fake.app="recorded"
 live:=buildTestCampaignRecoveryAssessment(context.Background(),plan,cp,status,scenarios,options,nil,fake)
 if !assessmentHasExecute(live)||fake.deletes!=0{t.Fatal("matching live identity missing or assessment deleted it")}
 if live.Batches[0].Commands[0].Argv[0]!="gregale"||
  !strings.Contains(live.Batches[0].Commands[0].POSIXShell,"'\\''"){
  t.Fatal("generated commands lost argv or POSIX quoting")
 }
}

func TestRecoveryAssessmentSuppressesAdviceForLockOrChangingSnapshot(t *testing.T){
 // adr: 984 — stale ownership and unstable evidence cannot produce actionable commands.
 plan,cp,scenarios,options:=recoveryAssessmentJournalFixture(t)
 fake:=&campaignCleanupIdentityFake{campaignCleanupFake:campaignCleanupFake{app:"recorded"}}
 locked:=buildTestCampaignRecoveryAssessment(context.Background(),plan,cp,testCampaignStatusReport{SnapshotStable:true,LockPresent:true},
  scenarios,options,nil,fake)
 if assessmentHasExecute(locked)||len(locked.Commands)!=0||len(locked.Batches[0].Commands)!=0{t.Fatal("locked campaign produced recovery commands")}
 result:=buildTestCampaignRecoveryAssessment(context.Background(),plan,cp,testCampaignStatusReport{SnapshotStable:true},scenarios,options,nil,fake)
 invalidateTestCampaignRecoveryAssessment(&result)
 if result.SnapshotStable||assessmentHasExecute(result)||len(result.Commands)!=0||len(result.Batches[0].Commands)!=0{
  t.Fatal("unstable snapshot retained commands")
 }
}

func TestRecoveryAssessmentFingerprintIncludesJournalMembership(t *testing.T){
 // adr: 984 — a concurrent snapshot or changed lifecycle record invalidates advice.
 f:=newCampaignStatusTestFiles(t)
 options:=testCampaignRecoveryAssessmentOptions{Plan:f.planPath,Policy:f.policyPath,Manifest:f.manifestPath,Directory:f.directory}
 before,err:=testCampaignRecoveryAssessmentStamp(f.plan,options,nil)
 if err!=nil{t.Fatal(err)}
 batch:=f.plan.Batches[1]
 f.checkpoint.Batches[batch.ID]=testCampaignCheckpointBatch{Status:"running"}
 if err:=writeTestCampaignJSON(filepath.Join(f.directory,"checkpoint.json"),f.checkpoint);err!=nil{t.Fatal(err)}
 journal,err:=prepareTestCampaignJournal(f.directory,batch)
 if err!=nil{t.Fatal(err)}
 if _,err:=newTestCampaignRunJournal(journal,batch.Scenario,batch.Profile,strings.Repeat("c",32),f.scenarios[batch.Scenario]);err!=nil{t.Fatal(err)}
 after,err:=testCampaignRecoveryAssessmentStamp(f.plan,options,nil)
 if err!=nil||before==after{t.Fatal("new journal membership did not change snapshot fingerprint")}
}

func TestRecoveryAssessmentRejectsExportAlias(t *testing.T){
 // adr: 984 — exporting an assessment cannot overwrite a checkpoint through an alias.
 f:=newCampaignStatusTestFiles(t)
 alias:=filepath.Join(f.root,"alias.json")
 if err:=os.Link(filepath.Join(f.directory,"checkpoint.json"),alias);err!=nil{t.Fatal(err)}
 if code:=cmdTestCampaignRecover([]string{"--inspect","--plan",f.planPath,"--policy",f.policyPath,"--manifest",f.manifestPath,
  "--directory",f.directory,"--json",alias});code==0{t.Fatal("assessment export overwrote aliased checkpoint")}
}

func TestRecoveryAssessmentInspectionImportCannotSuggestMutations(t *testing.T){
 // adr: 984 — journal transport never grants recovery authority to an inspection-only recipient.
 f,bundle,_,_:=exportPortableJournalFixture(t,false)
 index,err:=importTestCampaignBundle(bundle,filepath.Join(f.root,"inspection"),false,"")
 if err!=nil{t.Fatal(err)}
 resolver,err:=loadTestCampaignBundleResolver(filepath.Join(index.Root,"bundle-index.json"))
 if err!=nil{t.Fatal(err)}
 options:=testCampaignRecoveryAssessmentOptions{Plan:filepath.Join(index.Root,"artifacts","plan.json"),
  Policy:f.policyPath,Manifest:f.manifestPath,Directory:filepath.Join(index.Root,"work"),Handoff:resolver.IndexPath,
  OutputDirectory:filepath.Join(f.root,"next-recovery")}
 fake:=&campaignCleanupIdentityFake{campaignCleanupFake:campaignCleanupFake{app:"journal-app"}}
 result:=buildTestCampaignRecoveryAssessment(context.Background(),f.plan,f.checkpoint,testCampaignStatusReport{SnapshotStable:true},
  f.scenarios,options,resolver,fake)
 if len(result.Commands)!=0||assessmentHasExecute(result){t.Fatal("inspection import suggested mutations")}
 for _,batch:=range result.Batches{if len(batch.Commands)!=0{t.Fatal("inspection import suggested a cleanup command")}}
 if fake.deletes!=0{t.Fatal("assessment deleted a session")}
}

func TestRecoveryAssessmentCanSuggestRefreshForMatchingBlockedSeal(t *testing.T){
 // adr: 984 — a blocked seal may be refreshed without reopening its scheduler or permitting cleanup.
 plan,cp,scenarios,options:=recoveryAssessmentJournalFixture(t)
 seal:=testCampaignRecoverySeal{SchemaVersion:1,Blocked:true,ParentPlanSHA256:testCampaignJSONHash(plan),CheckpointSHA256:testCampaignJSONHash(cp)}
 if err:=writeTestCampaignJSON(filepath.Join(options.Directory,testCampaignRecoverySealName),seal);err!=nil{t.Fatal(err)}
 result:=buildTestCampaignRecoveryAssessment(context.Background(),plan,cp,testCampaignStatusReport{SnapshotStable:true,Sealed:true},
  scenarios,options,nil,nil)
 if result.Recommendation!="refresh_blocked_reconciliation"||len(result.Commands)!=1{t.Fatal("matching blocked seal has no refresh advice")}
 for _,batch:=range result.Batches{if len(batch.Commands)!=0{t.Fatal("sealed directory was reopened for cleanup")}}
}
