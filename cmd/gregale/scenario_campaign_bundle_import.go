package main

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"path"
)

func cmdTestCampaignBundle(args []string) int {
	if len(args)>0 && args[0]=="export" { return cmdTestCampaignBundleExport(args[1:]) }
	if len(args)==0 || (args[0]!="inspect" && args[0]!="import") {
		return printErr("Invalid bundle command",errors.New("use test policy bundle export, inspect or import"))
	}
	mode:=args[0]
	fs:=newFlagSet("test policy bundle "+mode,flag.ContinueOnError)
	from:=fs.String("from","","portable bundle JSON")
	directory:=fs.String("directory","","new import directory")
	activate:=fs.Bool("activate",false,"activate this recipient through an exclusive shared handoff claim")
	claims:=fs.String("claim-directory","","existing shared directory used by all recipients of this transfer")
	if err:=fs.Parse(args[1:]);err!=nil{if errors.Is(err,flag.ErrHelp){return 0};return 1}
	if rejectUnexpectedFlagArgs(fs)||fs.NArg()!=0||*from==""||
		(mode=="import"&&*directory=="")||(mode=="inspect"&&(*directory!=""||*activate||*claims!=""))||(*activate!=(*claims!="")){
		return printErr("Invalid bundle options",errors.New("--from is required; import needs a new --directory; --activate and --claim-directory must be supplied together"))
	}
	bundle,err:=readTestCampaignBundle(*from)
	if err!=nil{return printErr("Invalid campaign bundle",err)}
	if mode=="inspect"{return writeTestCampaignBundleSummary(bundle)}
	index,err:=importTestCampaignBundle(bundle,*directory,*activate,*claims)
	if err!=nil{return printErr("Could not import campaign bundle",err)}
	result:=struct{
		BundleID string `json:"bundle_id"`
		Index string `json:"handoff_index"`
		Plan string `json:"plan"`
		Policy string `json:"policy"`
		Work string `json:"working_directory"`
		Active bool `json:"active"`
	}{bundle.ID,filepath.Join(index.Root,"bundle-index.json"),filepath.Join(index.Root,"artifacts","plan.json"),
		filepath.Join(index.Root,"artifacts","policy.yaml"),filepath.Join(index.Root,"work"),index.Active}
	if err:=json.NewEncoder(os.Stdout).Encode(result);err!=nil{return printErr("Could not write import summary",err)}
	return 0
}

func importTestCampaignBundle(bundle testCampaignBundle,directory string,active bool,claims string) (testCampaignBundleIndex,error) {
	var index testCampaignBundleIndex
	if err:=validateTestCampaignBundle(bundle);err!=nil{return index,err}
	root,err:=filepath.Abs(directory)
	if err!=nil{return index,err}
	if _,err:=os.Lstat(root);!os.IsNotExist(err){return index,errors.New("import directory must not exist")}
	if active {
		claims,err=filepath.Abs(claims)
		if err!=nil{return index,err}
		if err:=testCampaignPrivateDirectory(claims);err!=nil{return index,err}
	}
	stage,err:=os.MkdirTemp(filepath.Dir(root),".gregale-campaign-import-")
	if err!=nil{return index,err}
	defer func(){_ = os.RemoveAll(stage)}()
	index=testCampaignBundleIndex{SchemaVersion:1,BundleID:bundle.ID,Root:root,Active:active,Side:bundle.Side,Revision:bundle.Revision,PlanSHA256:bundle.PlanSHA256,HistoryComplete:bundle.HistoryComplete,Orphans:append([]testCampaignBundleOrphan{},bundle.Orphans...),
		Nodes:append([]testCampaignBundleNode{},bundle.Nodes...),Files:[]testCampaignBundleInventoryFile{}}
	for _,file:=range bundle.Files {
		name:=filepath.Join(stage,"artifacts",filepath.FromSlash(file.Path))
		if err:=os.MkdirAll(filepath.Dir(name),0o700);err!=nil{return index,err}
		if err:=os.WriteFile(name,file.Data,0o600);err!=nil{return index,err}
		index.Files=append(index.Files,testCampaignBundleInventoryFile{Path:file.Path,SHA256:file.SHA256})
	}
	// Validate policy semantics before any recipient ownership is claimed.
	if _,err:=readTestCampaignPolicy(filepath.Join(stage,"artifacts","policy.yaml"));err!=nil{return index,err}
	if err:=os.Mkdir(filepath.Join(stage,"work"),0o700);err!=nil{return index,err}
	for _,file:=range bundle.Files {
		if path.Dir(file.Path)!="nodes/0"{continue}
		if err:=os.WriteFile(filepath.Join(stage,"work",filepath.Base(file.Path)),file.Data,0o600);err!=nil{return index,err}
	}
	for _,orphan:=range bundle.Orphans{
		if err:=os.WriteFile(filepath.Join(stage,"work","batch-"+orphan.Batch+".log"),[]byte("Original log content excluded from campaign handoff.\n"),0o600);err!=nil{return index,err}
	}
	if active {
		index.HostSHA256=testCampaignBundleHost()
		index.Nonce,err=testCampaignBundleNonce()
		if err!=nil{return index,err}
		index.ClaimPath=filepath.Join(claims,bundle.ID+".json")
	}
	owner:=testCampaignBundleOwner{SchemaVersion:1,BundleID:bundle.ID,IndexSHA256:testCampaignJSONHash(index)}
	if err:=writeTestCampaignJSON(filepath.Join(stage,"work",testCampaignImportName),owner);err!=nil{return index,err}
	for _,node:=range bundle.Nodes {
		if err:=writeTestCampaignJSON(filepath.Join(stage,"artifacts",filepath.FromSlash(node.Path),testCampaignImportName),owner);err!=nil{return index,err}
	}
	if err:=writeTestCampaignJSON(filepath.Join(stage,"bundle-index.json"),index);err!=nil{return index,err}
	var claimMade bool
	published:=false
	defer func(){if claimMade&&!published{_ = os.Remove(index.ClaimPath)}}()
	if active {
		claim:=testCampaignBundleClaim{SchemaVersion:1,BundleID:bundle.ID,Root:root,IndexSHA256:testCampaignJSONHash(index),Nonce:index.Nonce}
		body,err:=json.Marshal(claim)
		if err!=nil{return index,err}
		file,err:=os.OpenFile(index.ClaimPath,os.O_CREATE|os.O_EXCL|os.O_WRONLY,0o600)
		if err!=nil{return index,errors.New("this transfer already has a recipient claim or the claim directory is unavailable")}
		claimMade=true
		_,writeErr:=file.Write(body)
		if writeErr==nil{writeErr=file.Sync()}
		closeErr:=file.Close()
		if writeErr!=nil{return index,writeErr}
		if closeErr!=nil{return index,closeErr}
	}
	// A sibling staging directory provides an atomic publication boundary.
	if _,err:=os.Lstat(root);!os.IsNotExist(err){return index,errors.New("import destination appeared while staging")}
	if err:=os.Rename(stage,root);err!=nil{return index,err}
	published=true
	return index,nil
}
