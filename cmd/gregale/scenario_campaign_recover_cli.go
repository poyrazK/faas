package main

import (
 "context"
 "encoding/json"
 "errors"
 "flag"
 "os"
 "path/filepath"
 "strings"
 "time"
)

func cmdTestCampaignRecover(args []string)int{
 fs:=newFlagSet("test policy recover",flag.ContinueOnError)
 inspect:=fs.Bool("inspect",false,"read-only recovery assessment (required)")
 planPath:=fs.String("plan","","saved campaign plan")
 policyPath:=fs.String("policy","","bound campaign policy")
 manifestPath:=fs.String("manifest","gregale-test.yaml","bound manifest")
 directory:=fs.String("directory","","campaign work directory")
 handoff:=fs.String("handoff","","portable import index")
 from:=fs.String("from","","exact original current report")
 baseline:=fs.String("baseline","","exact original baseline report")
 recovery:=fs.String("recovery-bundle","","optional matching reconciliation bundle")
 output:=fs.String("output-directory","","new directory to use in suggested reconciliation command; never created")
 jsonPath:=fs.String("json","","optional assessment export outside campaign directories")
 live:=fs.Bool("check-cleanup",false,"inspect live sessions using read-only GET requests")
 if err:=fs.Parse(args);err!=nil{if errors.Is(err,flag.ErrHelp){return 0};return 1}
 if rejectUnexpectedFlagArgs(fs)||fs.NArg()!=0||!*inspect||*planPath==""||*policyPath==""||*directory==""{
  return printErr("Invalid recovery assessment options",errors.New("--inspect, --plan, --policy and --directory are required"))
 }
 resolver,err:=loadTestCampaignBundleResolver(*handoff)
 if err!=nil{return printErr("Invalid handoff",err)}
 if err:=enforceTestCampaignPlanHandoff(*planPath,resolver);err!=nil{return printErr("Imported plan requires its handoff index",err)}
 *directory,err=filepath.Abs(*directory)
 if err!=nil{return printErr("Invalid campaign directory",err)}
 if err:=testCampaignPrivateDirectory(*directory);err!=nil{return printErr("Invalid campaign directory",err)}
 if *output==""{*output=*directory+"-recovery"}
 *output,err=filepath.Abs(*output)
 if err!=nil{return printErr("Invalid suggested recovery directory",err)}
 o:=testCampaignRecoveryAssessmentOptions{*planPath,*policyPath,*manifestPath,*directory,*handoff,*from,*baseline,*output,*recovery}
 var plan testCampaignPolicyPlan
 if err:=readTestCampaignJSON(*planPath,&plan);err!=nil{return printErr("Invalid plan",err)}
 policy,err:=readTestCampaignPolicy(*policyPath)
 if err!=nil{return printErr("Invalid policy",err)}
 manifest,_,err:=readTestManifestDocument(*manifestPath,func(_ string,s testScenario)[]string{return testHTTPDataFields(s)})
 if err!=nil{return printErr("Invalid manifest",err)}
 check:=plan;check.Recovery=nil
 if err:=validateTestCampaignRunnable(check,manifest.Scenarios,policy);err!=nil{return printErr("Assessment binding changed",err)}
 var cp testCampaignCheckpoint
 if err:=readTestCampaignJSON(filepath.Join(*directory,"checkpoint.json"),&cp);err!=nil{return printErr("Invalid checkpoint",err)}
 if err:=validateTestCampaignAssessmentRevision(cp);err!=nil{return printErr("Invalid checkpoint",err)}
 var before,after []testRunReceipt
 for _,input:=range []struct{path,hash string;receipts *[]testRunReceipt}{
  {*from,plan.Execution.CurrentSHA256,&after},{*baseline,plan.Execution.BaselineSHA256,&before},
 }{
  if (input.path=="")!=(input.hash==""){return printErr("Original report changed",errors.New("supply exactly the reports used to plan"))}
  if input.path!=""{
   receipts,hash,err:=readTestBaselineReport(input.path,nil)
   if err!=nil||hash!=input.hash{return printErr("Original report changed",errors.New("original report hash differs or is unreadable"))}
   *input.receipts=receipts
  }
 }
 inputs:=[]string{*planPath,*policyPath,*manifestPath,*from,*baseline}
 for _,name:=range []string{"checkpoint.json",testCampaignRecoverySealName,testCampaignHandoffName,testCampaignCancelName,
  testCampaignImportName,testCampaignContinuationOwnerName,"runner.lock","policy-result.json","combined-current.json","combined-baseline.json"}{
  inputs=append(inputs,filepath.Join(*directory,name))
 }
 if *recovery!=""{
  entries,err:=os.ReadDir(*recovery)
  if err!=nil{return printErr("Invalid recovery bundle",err)}
  for _,entry:=range entries{if !entry.IsDir(){inputs=append(inputs,filepath.Join(*recovery,entry.Name()))}}
 }
 directories:=[]string{*directory,*recovery}
 if resolver!=nil{directories=append(directories,resolver.Index.Root);inputs=append(inputs,resolver.IndexPath,resolver.Index.ClaimPath,resolver.Index.ClaimPath+".lock")}
 if err:=protectTestCampaignStatusOutputs([]string{*jsonPath},inputs,directories);err!=nil{return printErr("Invalid assessment output",err)}
 stamp,err:=testCampaignRecoveryAssessmentStamp(plan,o,resolver)
 if err!=nil{return printErr("Could not capture recovery evidence",err)}
 status,err:=buildTestCampaignStatus(plan,&cp,*directory,"",policy,manifest.Scenarios,before,after,resolver)
 if err!=nil{return printErr("Could not inspect campaign status",err)}
 if *recovery!=""{
  if err:=applyTestCampaignStatusRecovery(&status,plan,cp,*recovery,policy,manifest.Scenarios,resolver);err!=nil{return printErr("Invalid recovery bundle",err)}
 }
 for _,parent:=range status.Lineage{
  physical,err:=resolveTestCampaignDirectory(parent.Directory,resolver)
  if err!=nil{return printErr("Invalid recovery parent",err)}
  directories=append(directories,physical)
  inputs=append(inputs,filepath.Join(physical,"checkpoint.json"),filepath.Join(physical,testCampaignRecoverySealName))
 }
 // Protect stdout and explicit exports against aliases to every inspected
 // report and journal, even when the export path is outside the directory.
 for _,batch:=range plan.Batches{
  inputs=append(inputs,filepath.Join(*directory,"batch-"+batch.ID+".json"),filepath.Join(*directory,"batch-"+batch.ID+".log"))
  journal:=testCampaignJournalDirectory(*directory,batch.ID)
  if entries,err:=os.ReadDir(journal);err==nil{for _,entry:=range entries{inputs=append(inputs,filepath.Join(journal,entry.Name()))}}
 }
 if err:=protectTestCampaignStatusOutputs([]string{*jsonPath},inputs,directories);err!=nil{return printErr("Invalid assessment output",err)}
 var reader testCampaignCleanupReader
 if *live{
  client,err:=authedClient()
  if err!=nil{return printErr("Could not authenticate read-only cleanup inspection",err)}
  reader=client
 }
 ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second)
 defer cancel()
 result:=buildTestCampaignRecoveryAssessment(ctx,plan,cp,status,manifest.Scenarios,o,resolver,reader)
 // A suggested destination cannot contain an input, or overlap an owned or
 // immutable campaign directory. It is never created by this assessment.
 for _,name:=range append(append([]string{},inputs...),directories...){
  if name==""{continue}
  absolute,err:=filepath.Abs(name)
  if err!=nil{continue}
  if testCampaignRecoveryPathContains(o.OutputDirectory,absolute)||testCampaignRecoveryPathContains(absolute,o.OutputDirectory){
   result.Commands=nil
   result.Blockers=append(result.Blockers,"Suggested recovery output overlaps campaign evidence or inputs; supply a distinct new --output-directory.")
   break
  }
 }
 if status.Sealed&&*recovery!=""{
  var continuation testCampaignPolicyPlan
  path:=filepath.Join(*recovery,"continuation-plan.json")
  if readTestCampaignJSON(path,&continuation)==nil&&continuation.Recovery!=nil&&continuation.Execution!=nil&&!continuation.Recovery.Blocked{
   result.Recommendation="inspect_continuation"
   command:=[]string{"gregale","test","policy","status","--plan",path,"--policy",*policyPath,"--manifest",*manifestPath}
   if continuation.Execution.CurrentSHA256!=""{command=append(command,"--from",filepath.Join(*recovery,"current.json"))}
   if continuation.Execution.BaselineSHA256!=""{command=append(command,"--baseline",filepath.Join(*recovery,"baseline.json"))}
   if *handoff!=""{command=append(command,"--handoff",*handoff)}
   result.Commands=append(result.Commands,newTestCampaignRecoveryCommand("Inspect the verified continuation",false,command...))
  }
 }
 fresh,stampErr:=testCampaignRecoveryAssessmentStamp(plan,o,resolver)
 if stampErr!=nil||fresh!=stamp||!testCampaignRecoveryBindingsStable(o,plan,cp,policy,manifest.Scenarios){
  invalidateTestCampaignRecoveryAssessment(&result)
 }
 if *jsonPath!=""{if err:=writeTestCampaignJSON(*jsonPath,result);err!=nil{return printErr("Could not export assessment",err)}}
 encoder:=json.NewEncoder(os.Stdout);encoder.SetIndent("","  ")
 if err:=encoder.Encode(result);err!=nil{return printErr("Could not write assessment",err)}
 return 0
}

func testCampaignRecoveryPathContains(root,name string)bool{
 relative,err:=filepath.Rel(root,name)
 return err==nil&&(relative=="."||(relative!=".."&&!strings.HasPrefix(relative,".."+string(filepath.Separator))))
}
func invalidateTestCampaignRecoveryAssessment(result *testCampaignRecoveryAssessment){
 result.SnapshotStable=false;result.Status.SnapshotStable=false
 result.Recommendation="rerun_assessment"
 result.Commands=[]testCampaignRecoveryCommand{}
 for i:=range result.Batches{result.Batches[i].Commands=[]testCampaignRecoveryCommand{}}
 result.Blockers=append(result.Blockers,"Evidence or ownership changed while inspected. Rerun the assessment; all suggested commands were suppressed.")
}

func testCampaignRecoveryBindingsStable(o testCampaignRecoveryAssessmentOptions,plan testCampaignPolicyPlan,cp testCampaignCheckpoint,policy testCampaignPolicy,scenarios map[string]testScenario)bool{
 var freshPlan testCampaignPolicyPlan
 var freshCheckpoint testCampaignCheckpoint
 if readTestCampaignJSON(o.Plan,&freshPlan)!=nil||readTestCampaignJSON(filepath.Join(o.Directory,"checkpoint.json"),&freshCheckpoint)!=nil{
  return false
 }
 freshPolicy,err:=readTestCampaignPolicy(o.Policy)
 if err!=nil{return false}
 manifest,_,err:=readTestManifestDocument(o.Manifest,func(_ string,s testScenario)[]string{return testHTTPDataFields(s)})
 return err==nil&&testCampaignJSONHash(freshPlan)==testCampaignJSONHash(plan)&&testCampaignJSONHash(freshCheckpoint)==testCampaignJSONHash(cp)&&
  testCampaignJSONHash(freshPolicy)==testCampaignJSONHash(policy)&&testCampaignJSONHash(manifest.Scenarios)==testCampaignJSONHash(scenarios)
}
