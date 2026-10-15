package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const testCampaignHandoffName = "campaign-handoff.json"
const testCampaignImportName = "campaign-import.json"
const testCampaignBundleRawLimit = 64*1024*1024

type testCampaignBundleFile struct {
	Path string `json:"path"`
	SHA256 string `json:"sha256"`
	Data []byte `json:"data"`
}
type testCampaignBundleNode struct {
	Key string `json:"key"`
	Path string `json:"path"`
}
type testCampaignBundleOrphan struct {
	Batch string `json:"batch"`
}
type testCampaignBundle struct {
	SchemaVersion int `json:"schema_version"`
	SourceFrozen bool `json:"source_frozen"`
	ID string `json:"bundle_id"`
	PlanSHA256 string `json:"plan_sha256"`
	Side string `json:"side"`
	Revision string `json:"revision"`
	HistoryComplete bool `json:"history_complete"`
	Orphans []testCampaignBundleOrphan `json:"orphan_log_markers,omitempty"`
	Nodes []testCampaignBundleNode `json:"nodes"`
	Files []testCampaignBundleFile `json:"files"`
}
type testCampaignBundleInventoryFile struct {
	Path string `json:"path"`
	SHA256 string `json:"sha256"`
}
type testCampaignBundleIndex struct {
	PlanSHA256 string `json:"plan_sha256"`
	HistoryComplete bool `json:"history_complete"`
	Orphans []testCampaignBundleOrphan `json:"orphan_log_markers,omitempty"`
	SchemaVersion int `json:"schema_version"`
	BundleID string `json:"bundle_id"`
	Root string `json:"root"`
	Active bool `json:"active"`
	Side string `json:"side"`
	Revision string `json:"revision"`
	HostSHA256 string `json:"host_sha256,omitempty"`
	ClaimPath string `json:"claim_path,omitempty"`
	Nonce string `json:"nonce,omitempty"`
	Nodes []testCampaignBundleNode `json:"nodes"`
	Files []testCampaignBundleInventoryFile `json:"files"`
}
type testCampaignBundleClaim struct {
	Transferred string `json:"transferred_to_bundle,omitempty"`
	Work map[string]string `json:"plan_work_directories,omitempty"`
	SchemaVersion int `json:"schema_version"`
	BundleID string `json:"bundle_id"`
	Root string `json:"root"`
	IndexSHA256 string `json:"index_sha256"`
	Nonce string `json:"nonce"`
}
type testCampaignBundleResolver struct {
	Index testCampaignBundleIndex
	IndexPath string
}
type testCampaignBundleOwner struct {
	SchemaVersion int `json:"schema_version"`
	BundleID string `json:"bundle_id"`
	IndexSHA256 string `json:"index_sha256"`
}
type testCampaignHandoffSeal struct {
	SchemaVersion int `json:"schema_version"`
	BundleID string `json:"bundle_id"`
	PlanSHA256 string `json:"plan_sha256"`
	CheckpointSHA256 string `json:"checkpoint_sha256"`
}

func testCampaignBundleHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func testCampaignBundleID(bundle testCampaignBundle) string {
	bundle.ID = ""
	bundle.Files = append([]testCampaignBundleFile{},bundle.Files...)
	for i := range bundle.Files { bundle.Files[i].Data = nil }
	return testCampaignJSONHash(bundle)
}
func readTestCampaignBundleBytes(name string,limit int64) ([]byte,error) {
	file,err := openCustomerFile(name)
	if err != nil { return nil,err }
	defer func(){ _ = file.Close() }()
	body,err := io.ReadAll(io.LimitReader(file,limit+1))
	if err != nil { return nil,err }
	if int64(len(body)) > limit { return nil,errors.New("campaign bundle artifact exceeds its size limit") }
	return body,nil
}
func decodeTestCampaignBundleJSON(body []byte,value any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil { return err }
	if decoder.Decode(new(any)) != io.EOF { return errors.New("expected one bundle document") }
	return nil
}

var testCampaignBundleBatchName = regexp.MustCompile(`^batch-[A-Za-z0-9_-]+\.json$`)
func testCampaignBundlePathAllowed(name string) bool {
	if name == "" || path.Clean(name) != name || strings.Contains(name,"\\") || path.IsAbs(name) { return false }
	switch name {
	case "plan.json","policy.yaml","current.json","baseline.json":
		return true
	case "recovery/reconciliation.json","recovery/continuation-plan.json","recovery/current.json","recovery/baseline.json":
		return true
	}
	parts := strings.Split(name,"/")
	if len(parts) == 2 && parts[0] == "recovery" {
		return regexp.MustCompile(`^recovered-batch-[1-9][0-9]*\.json$`).MatchString(parts[1])
	}
	if _,_,ok:=testCampaignPortableJournalPath(name);ok{return true}
	if len(parts) != 3 || parts[0] != "nodes" || !regexp.MustCompile(`^(0|[1-9][0-9]*)$`).MatchString(parts[1]) { return false }
	return parts[2] == "checkpoint.json" || parts[2] == testCampaignRecoverySealName || testCampaignBundleBatchName.MatchString(parts[2])
}

// Preserve evidence bytes and their hashes. Refuse recognized credentials
// rather than silently altering assertions, provenance or source identities.
func checkTestCampaignBundleSensitive(body []byte,known []string) error {
	text := strings.ToLower(string(body))
	for _,marker := range []string{"-----begin private key-----","-----begin rsa private key-----","authorization: bearer ","authorization: basic "} {
		if strings.Contains(text,marker) { return errors.New("artifact contains credential material; create redacted evidence before export") }
	}
	var raw any
	if json.Unmarshal(body,&raw) != nil { if yaml.Unmarshal(body,&raw) != nil { return errors.New("bundle artifact is not structured evidence") } }
	var walk func(any) error
	walk = func(value any) error {
		switch value := value.(type) {
		case string:
			for _,secret := range known {
				if secret != "" && ((len(secret) >= 4 && strings.Contains(value,secret)) || value == secret) {
					return errors.New("artifact contains a configured secret value")
				}
			}
			lower := strings.ToLower(strings.TrimSpace(value))
			if strings.HasPrefix(lower,"bearer ") || strings.HasPrefix(lower,"basic ") ||
				regexp.MustCompile(`[a-z][a-z0-9+.-]*://[^/\s:@]+:[^/\s@]+@`).MatchString(value) {
				return errors.New("artifact contains a credential-bearing value")
			}
		case []any:
			for _,child := range value { if err := walk(child); err != nil { return err } }
		case map[string]any:
			for key,child := range value {
				key = strings.ToLower(strings.ReplaceAll(key,"-","_"))
				switch key {
				case "secrets","secret","password","credentials","authorization","proxy_authorization","cookie","set_cookie","api_key","token","access_token","refresh_token","client_secret","private_key","stdout","stderr","body","request_body","response_body","headers","request_headers","response_headers","diagnostics","apikey","accesstoken","refreshtoken","clientsecret","privatekey","proxyauthorization","setcookie","requestbody","responsebody","requestheaders","responseheaders","run_secret","runsecret":
					redacted:=false
					if value,ok:=child.(string);ok{lower:=strings.ToLower(strings.TrimSpace(value));redacted=lower=="[redacted]"||lower=="<redacted>"}
					if !redacted && child != nil && fmt.Sprint(child) != "" && fmt.Sprint(child) != "map[]" && fmt.Sprint(child) != "[]" {
						return errors.New("artifact contains credential configuration or raw payload/diagnostic fields; export shareable evidence without those captures")
					}
				}
				if err := walk(child); err != nil { return err }
			}
		}
		return nil
	}
	return walk(raw)
}
func validateTestCampaignBundle(bundle testCampaignBundle) error {
	if bundle.SchemaVersion != 1 || !bundle.SourceFrozen || len(bundle.Nodes) == 0 || len(bundle.Nodes) > 33 || len(bundle.Files) == 0 || len(bundle.Files) > 4096 {
		return errors.New("unsupported or incomplete campaign bundle")
	}
	seen := make(map[string]bool)
	var total int64
	for _,file := range bundle.Files {
		if !testCampaignBundlePathAllowed(file.Path) || seen[file.Path] || file.SHA256 != testCampaignBundleHash(file.Data) {
			return errors.New("bundle has an invalid path, duplicate file or changed artifact")
		}
		if strings.HasSuffix(file.Path,".json") { if err:=checkTestCampaignBundleJSONKeys(file.Data);err!=nil{return err} }
		if err := checkTestCampaignBundleSensitive(file.Data,nil); err != nil { return err }
		seen[file.Path] = true
		total += int64(len(file.Data))
		if total > testCampaignBundleRawLimit { return errors.New("bundle exceeds 64 MiB of decoded artifacts") }
	}
	keys := make(map[string]bool)
	for i,node := range bundle.Nodes {
		if node.Key == "" || keys[node.Key] || node.Path != fmt.Sprintf("nodes/%d",i) || !seen[node.Path+"/checkpoint.json"] {
			return errors.New("bundle node references are incomplete or ambiguous")
		}
		keys[node.Key] = true
	}
	if !seen["plan.json"] || !seen["policy.yaml"] || bundle.ID == "" || bundle.ID != testCampaignBundleID(bundle) {
		return errors.New("bundle inventory identity does not match")
	}
	if err:=validateTestCampaignBundleGraph(bundle);err!=nil{return err}
 files:=make(map[string][]byte)
 for _,file:=range bundle.Files{files[file.Path]=file.Data}
 return validateTestCampaignPortableJournals(bundle,files)
}
func readTestCampaignBundle(name string) (testCampaignBundle,error) {
	var bundle testCampaignBundle
	body,err := readTestCampaignBundleBytes(name,96*1024*1024)
	if err != nil { return bundle,err }
	if err := decodeTestCampaignBundleJSON(body,&bundle); err != nil { return bundle,err }
	return bundle,validateTestCampaignBundle(bundle)
}

func loadTestCampaignBundleResolver(indexPath string) (*testCampaignBundleResolver,error) {
	if indexPath == "" { return nil,nil }
	indexPath,err := filepath.Abs(indexPath)
	if err != nil { return nil,err }
	resolver := &testCampaignBundleResolver{IndexPath:indexPath}
	if err := readTestCampaignJSON(indexPath,&resolver.Index); err != nil { return nil,err }
	index := resolver.Index
	if index.SchemaVersion != 1 || filepath.Join(index.Root,"bundle-index.json") != indexPath || len(index.Files) > 4096 || len(index.Nodes) == 0 || len(index.Nodes) > 33 {
		return nil,errors.New("invalid or relocated import index")
	}
	seen := make(map[string]bool)
	var total int64
	snapshot:=testCampaignBundle{SchemaVersion:1,SourceFrozen:true,ID:index.BundleID,PlanSHA256:index.PlanSHA256,Side:index.Side,Revision:index.Revision,HistoryComplete:index.HistoryComplete,Orphans:index.Orphans,Nodes:index.Nodes}
	for _,file := range index.Files {
		if !testCampaignBundlePathAllowed(file.Path) || seen[file.Path] { return nil,errors.New("invalid import artifact path") }
		seen[file.Path] = true
		body,err := readTestCampaignBundleBytes(filepath.Join(index.Root,"artifacts",filepath.FromSlash(file.Path)),testCampaignBundleRawLimit)
		if err != nil || testCampaignBundleHash(body) != file.SHA256 { return nil,errors.New("import artifact is missing or changed") }
		snapshot.Files=append(snapshot.Files,testCampaignBundleFile{Path:file.Path,SHA256:file.SHA256,Data:body})
		total += int64(len(body))
		if total > testCampaignBundleRawLimit { return nil,errors.New("import inventory exceeds its size allowance") }
	}
	if !seen["plan.json"] || !seen["policy.yaml"] { return nil,errors.New("import inventory is incomplete") }
	keys := make(map[string]bool)
	for i,node := range index.Nodes {
		if node.Key == "" || keys[node.Key] || node.Path != fmt.Sprintf("nodes/%d",i) || !seen[node.Path+"/checkpoint.json"] { return nil,errors.New("invalid portable parent reference") }
		keys[node.Key] = true
	}
	if err:=validateTestCampaignBundle(snapshot);err!=nil{return nil,err}
	return resolver,nil
}
func (resolver *testCampaignBundleResolver) requireActive() error { return resolver.requireClaim(false) }
func (resolver *testCampaignBundleResolver) requireClaim(allowTransferred bool) error {
	if resolver == nil { return nil }
	index := resolver.Index
	if !index.Active || index.ClaimPath == "" || index.Nonce == "" || index.HostSHA256 != testCampaignBundleHost() { return errors.New("handoff import is inspection-only; execution requires an activated claim") }
	var claim testCampaignBundleClaim
	if err := readTestCampaignJSON(index.ClaimPath,&claim); err != nil { return errors.New("handoff claim is unavailable") }
	if claim.SchemaVersion != 1 || claim.BundleID != index.BundleID || claim.Root != index.Root ||
		claim.IndexSHA256 != testCampaignJSONHash(index) || claim.Nonce != index.Nonce { return errors.New("handoff claim does not identify this import") }
	if !allowTransferred{
		if claim.Transferred!=""{return errors.New("recipient ownership was handed off to another bundle")}
		for _,directory:=range claim.Work{if _,err:=os.Lstat(filepath.Join(directory,testCampaignHandoffName));!os.IsNotExist(err){return errors.New("a recipient working directory is frozen for handoff")}}
	}
	return nil
}
func (resolver *testCampaignBundleResolver) owner() testCampaignBundleOwner {
	return testCampaignBundleOwner{SchemaVersion:1,BundleID:resolver.Index.BundleID,IndexSHA256:testCampaignJSONHash(resolver.Index)}
}
func (resolver *testCampaignBundleResolver) resolve(key string) (string,error) {
	if resolver == nil { return key,nil }
	for _,node := range resolver.Index.Nodes {
		if node.Key == key { return filepath.Join(resolver.Index.Root,"artifacts",filepath.FromSlash(node.Path)),nil }
	}
	var owner testCampaignBundleOwner
	if readTestCampaignJSON(filepath.Join(key,testCampaignImportName),&owner) == nil && owner == resolver.owner() { return key,nil }
	return "",errors.New("parent reference is not part of this import or its owned working directories")
}
func resolveTestCampaignDirectory(key string,resolvers ...*testCampaignBundleResolver) (string,error) {
	if len(resolvers) > 1 { return "",errors.New("only one handoff resolver is supported") }
	if len(resolvers) == 1 && resolvers[0] != nil { return resolvers[0].resolve(key) }
	return key,nil
}
func checkTestCampaignBundleWork(directory string,resolver *testCampaignBundleResolver,write bool) error {
	if _,err := os.Lstat(filepath.Join(directory,testCampaignHandoffName)); !os.IsNotExist(err) {
		return errors.New("campaign ownership was handed off; use the recipient import")
	}
	var owner testCampaignBundleOwner
	err := readTestCampaignJSON(filepath.Join(directory,testCampaignImportName),&owner)
	if err == nil {
		if resolver == nil || owner != resolver.owner() { return errors.New("working directory belongs to another handoff import") }
	} else if !os.IsNotExist(err) { return err }
	if resolver != nil && write {
		if testCampaignBundleImmutableDirectory(directory,resolver) { return errors.New("imported evidence is immutable; use its work directory or a fresh runtime directory") }
		return resolver.requireActive()
	}
	return nil
}
func bindTestCampaignBundleWork(directory string,resolver *testCampaignBundleResolver) error {
	if resolver == nil { return nil }
	if err := checkTestCampaignBundleWork(directory,resolver,true); err != nil { return err }
	return writeTestCampaignJSON(filepath.Join(directory,testCampaignImportName),resolver.owner())
}
func testCampaignBundleNonce() (string,error) {
	var bytes [32]byte
	if _,err := rand.Read(bytes[:]); err != nil { return "",err }
	return hex.EncodeToString(bytes[:]),nil
}
func sortedTestCampaignBundleFiles(files []testCampaignBundleFile) {
	sort.Slice(files,func(i,j int) bool { return files[i].Path < files[j].Path })
}

func validateTestCampaignBundleGraph(bundle testCampaignBundle) error {
	files := make(map[string][]byte)
	for _,file := range bundle.Files { files[file.Path] = file.Data }
	var plan testCampaignPolicyPlan
	if decodeTestCampaignBundleJSON(files["plan.json"],&plan) != nil || plan.SchemaVersion != 1 || plan.Costs == nil || plan.Execution == nil ||
		testCampaignJSONHash(plan) != bundle.PlanSHA256 { return errors.New("bundle plan identity is invalid") }
	var policy testCampaignPolicy
	decoder := yaml.NewDecoder(bytes.NewReader(files["policy.yaml"]))
	decoder.KnownFields(true)
	if decoder.Decode(&policy) != nil || decoder.Decode(new(any)) != io.EOF || testCampaignJSONHash(policy) != plan.Execution.PolicySHA256 {
		return errors.New("bundle policy does not match the plan")
	}
	for _,input := range []struct{name,hash string}{{"current.json",plan.Execution.CurrentSHA256},{"baseline.json",plan.Execution.BaselineSHA256}} {
		body,exists := files[input.name]
		if (input.hash != "") != exists || (exists && testCampaignBundleHash(body) != input.hash) { return errors.New("bundle omits or changes an original report") }
	}
	var root testCampaignCheckpoint
	expectedHistoryComplete:=true
	link,childHash := plan.Recovery,bundle.PlanSHA256
	for i,node := range bundle.Nodes {
		var checkpoint testCampaignCheckpoint
		if decodeTestCampaignBundleJSON(files[node.Path+"/checkpoint.json"],&checkpoint) != nil || checkpoint.SchemaVersion != 1 || checkpoint.Batches == nil ||
			(checkpoint.Side != "current" && checkpoint.Side != "baseline") { return errors.New("bundle contains an invalid checkpoint") }
		revision,err := hex.DecodeString(checkpoint.Revision)
		if err != nil || (len(revision)!=20 && len(revision)!=32) { return errors.New("bundle revision is invalid") }
		if i==0 {
			root = checkpoint
			if checkpoint.PlanSHA256 != bundle.PlanSHA256 || checkpoint.Side != bundle.Side || checkpoint.Revision != bundle.Revision ||
				!testCampaignCheckpointLineageMatches(checkpoint,plan) { return errors.New("root checkpoint does not match the bundle") }
			allowed := make(map[string]bool)
			for _,batch := range plan.Batches { if batch.Side == checkpoint.Side { allowed[batch.ID] = true } }
			for id := range checkpoint.Batches { if !allowed[id] { return errors.New("root checkpoint contains an unplanned batch") } }
		} else {
			var seal testCampaignRecoverySeal
			if link == nil || link.ParentDirectory != node.Key || checkpoint.PlanSHA256 != link.ParentPlanSHA256 ||
				testCampaignJSONHash(checkpoint) != link.CheckpointSHA256 ||
				decodeTestCampaignBundleJSON(files[node.Path+"/"+testCampaignRecoverySealName],&seal) != nil ||
				seal.SchemaVersion != 1 || seal.ParentPlanSHA256 != link.ParentPlanSHA256 || seal.CheckpointSHA256 != link.CheckpointSHA256 ||
				seal.ContinuationSHA256 != childHash || seal.ReconciliationSHA256 != link.ReconciliationSHA256 || seal.Blocked != link.Blocked {
				return errors.New("bundle parent graph or seal is incomplete")
			}
			childHash = checkpoint.PlanSHA256
			if checkpoint.LineageVersion == 1 { link = checkpoint.Recovery } else { link = nil;expectedHistoryComplete=false }
		}
		for id,saved := range checkpoint.Batches {
			if saved.Status != "done" && saved.Status != "running" && saved.Status != "uncertain" { return errors.New("bundle checkpoint status is invalid") }
			name := "batch-"+id+".json"
			if !testCampaignBundleBatchName.MatchString(name) { return errors.New("invalid bundle batch ID") }
			body,exists := files[node.Path+"/"+name]
			if saved.Status == "done" && (!exists || saved.ReportSHA256 != testCampaignBundleHash(body)) { return errors.New("bundle is missing completed execution evidence") }
		}
	}
	if link != nil { return errors.New("bundle omits a referenced parent") }
	if bundle.HistoryComplete!=expectedHistoryComplete{return errors.New("bundle history completeness disagrees with checkpoint metadata")}
	seenOrphans:=make(map[string]bool)
	for _,orphan:=range bundle.Orphans{
		found:=false
		for _,batch:=range plan.Batches{if batch.ID==orphan.Batch&&batch.Side==root.Side{found=true}}
		_,recorded:=root.Batches[orphan.Batch]
		if !found||recorded||seenOrphans[orphan.Batch]{return errors.New("orphan log marker conflicts with checkpoint state")}
		seenOrphans[orphan.Batch]=true
	}
	if sealBody,exists := files["nodes/0/"+testCampaignRecoverySealName]; exists {
		var seal testCampaignRecoverySeal
		var reconciliation testCampaignReconciliation
		var continuation testCampaignPolicyPlan
		if decodeTestCampaignBundleJSON(sealBody,&seal) != nil || decodeTestCampaignBundleJSON(files["recovery/reconciliation.json"],&reconciliation) != nil ||
			decodeTestCampaignBundleJSON(files["recovery/continuation-plan.json"],&continuation) != nil ||
			seal.SchemaVersion != 1 || seal.ParentPlanSHA256 != bundle.PlanSHA256 || seal.CheckpointSHA256 != testCampaignJSONHash(root) ||
			seal.ReconciliationSHA256 != testCampaignJSONHash(reconciliation) || seal.ContinuationSHA256 != testCampaignJSONHash(continuation) ||
			reconciliation.ParentPlanSHA256 != bundle.PlanSHA256 || reconciliation.CheckpointSHA256 != seal.CheckpointSHA256 ||
			continuation.Execution == nil || continuation.Recovery == nil || continuation.Recovery.ParentDirectory != bundle.Nodes[0].Key {
			return errors.New("sealed root requires its matching recovery bundle")
		}
		for _,input := range []struct{name,hash string}{{"recovery/current.json",continuation.Execution.CurrentSHA256},{"recovery/baseline.json",continuation.Execution.BaselineSHA256}} {
			body,exists := files[input.name]
			if (input.hash != "") != exists || (exists && testCampaignBundleHash(body) != input.hash) { return errors.New("recovery report is missing or changed") }
		}
		entries := make(map[string]testCampaignRecoveryBatch)
		for _,entry := range reconciliation.Batches { if _,exists:=entries[entry.ID];exists{return errors.New("duplicate recovery batch")}; entries[entry.ID]=entry }
		for i,batch := range plan.Batches {
			if entry:=entries[batch.ID];entry.RecoveredSHA256!="" {
				body,exists:=files[fmt.Sprintf("recovery/recovered-batch-%d.json",i+1)]
				if !exists || testCampaignBundleHash(body)!=entry.RecoveredSHA256 { return errors.New("adopted recovery evidence is missing or changed") }
			}
		}
	} else {
		for name := range files { if strings.HasPrefix(name,"recovery/") { return errors.New("recovery artifacts have no root seal") } }
	}
	return nil
}

func testCampaignBundleHost() string {
	host,_:=os.Hostname()
	machine,_:=os.ReadFile("/etc/machine-id")
	return testCampaignBundleHash(append([]byte(host+"\n"),machine...))
}
func acquireTestCampaignHandoff(resolver *testCampaignBundleResolver,allowTransferred ...bool) (func(),error) {
	if resolver==nil{return func(){},nil}
	allow:=len(allowTransferred)==1&&allowTransferred[0]
	if err:=resolver.requireClaim(allow);err!=nil{return nil,err}
	lockPath:=resolver.Index.ClaimPath+".lock"
	file,err:=os.OpenFile(lockPath,os.O_CREATE|os.O_EXCL|os.O_WRONLY,0o600)
	if err!=nil{return nil,errors.New("handoff ownership is busy or its claim lock is stale")}
	_ = file.Close()
	return func(){_ = os.Remove(lockPath)},nil
}
func registerTestCampaignHandoffWork(resolver *testCampaignBundleResolver,planHash,directory,side,revision string) error {
	if resolver==nil{return nil}
	if err:=resolver.requireActive();err!=nil{return err}
	if side!=resolver.Index.Side || revision!=resolver.Index.Revision { return errors.New("handoff cannot change its revision side or commit") }
	var claim testCampaignBundleClaim
	if err:=readTestCampaignJSON(resolver.Index.ClaimPath,&claim);err!=nil{return err}
	if claim.Work==nil{claim.Work=make(map[string]string)}
	if existing:=claim.Work[planHash];existing!=""&&existing!=directory{return errors.New("this plan already owns another working directory; resume it instead of resetting execution")}
	claim.Work[planHash]=directory
	return writeTestCampaignJSON(resolver.Index.ClaimPath,claim)
}

func testCampaignBundleImmutableDirectory(directory string,resolver *testCampaignBundleResolver) bool {
	if resolver==nil{return false}
	relative,err:=filepath.Rel(filepath.Join(resolver.Index.Root,"artifacts"),directory)
	return err==nil&&relative!=".."&&!strings.HasPrefix(relative,".."+string(filepath.Separator))
}
func enforceTestCampaignPlanHandoff(planPath string,resolver *testCampaignBundleResolver) error {
	absolute,err:=filepath.Abs(planPath)
	if err!=nil{return err}
	directory:=filepath.Dir(absolute)
	for i:=0;i<32;i++{
		name:=filepath.Join(directory,"bundle-index.json")
		var index testCampaignBundleIndex
		if readTestCampaignJSON(name,&index)==nil&&index.SchemaVersion==1&&index.Root==directory&&index.BundleID!=""{
			if resolver==nil||resolver.IndexPath!=name{return errors.New("imported plans require their matching --handoff index")}
			return nil
		}
		next:=filepath.Dir(directory)
		if next==directory{break}
		directory=next
	}
	return nil
}

const testCampaignContinuationOwnerName="campaign-continuation-owner.json"
type testCampaignContinuationOwner struct {
	SchemaVersion int `json:"schema_version"`
	PlanSHA256 string `json:"plan_sha256"`
	Directory string `json:"directory"`
	HostSHA256 string `json:"host_sha256"`
}
func registerTestCampaignContinuationOwner(plan testCampaignPolicyPlan,directory string,resolver *testCampaignBundleResolver) error {
	if plan.Recovery==nil{return nil}
	if resolver!=nil{return resolver.requireActive()}
	parent:=plan.Recovery.ParentDirectory
	lockPath:=filepath.Join(parent,"runner.lock")
	lock,err:=os.OpenFile(lockPath,os.O_CREATE|os.O_EXCL|os.O_WRONLY,0o600)
	if err!=nil{return errors.New("parent campaign is busy or its execution lock is stale")}
	_ = lock.Close()
	defer func(){_ = os.Remove(lockPath)}()
	if err:=validateTestCampaignRecoverySeal(plan);err!=nil{return err}
	expected:=testCampaignContinuationOwner{SchemaVersion:1,PlanSHA256:testCampaignJSONHash(plan),Directory:directory,HostSHA256:testCampaignBundleHost()}
	name:=filepath.Join(parent,testCampaignContinuationOwnerName)
	file,err:=os.OpenFile(name,os.O_CREATE|os.O_EXCL|os.O_WRONLY,0o600)
	if os.IsExist(err){
		var existing testCampaignContinuationOwner
		if readTestCampaignJSON(name,&existing)!=nil||existing!=expected{return errors.New("continuation already owns another working directory; resume it instead")}
		return nil
	}
	if err!=nil{return err}
	body,err:=json.Marshal(expected)
	if err==nil{_,err=file.Write(body)}
	if err==nil{err=file.Sync()}
	closeErr:=file.Close()
	if err!=nil{return err}
	return closeErr
}
func revokeTestCampaignHandoff(resolver *testCampaignBundleResolver,bundleID string) error {
	if resolver==nil{return nil}
	var claim testCampaignBundleClaim
	if err:=readTestCampaignJSON(resolver.Index.ClaimPath,&claim);err!=nil{return err}
	if claim.Transferred!=""&&claim.Transferred!=bundleID{return errors.New("recipient was transferred to a different bundle")}
	claim.Transferred=bundleID
	return writeTestCampaignJSON(resolver.Index.ClaimPath,claim)
}

func checkTestCampaignBundleJSONKeys(body []byte) error {
	decoder:=json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var walk func(int) error
	walk=func(depth int)error{
		if depth>128{return errors.New("bundle JSON nesting exceeds the supported limit")}
		token,err:=decoder.Token()
		if err!=nil{return err}
		delimiter,container:=token.(json.Delim)
		if !container{return nil}
		switch delimiter{
		case '{':
			seen:=make(map[string]bool)
			for decoder.More(){
				token,err:=decoder.Token()
				if err!=nil{return err}
				key,ok:=token.(string)
				if !ok||seen[key]{return errors.New("bundle JSON contains duplicate object keys")}
				seen[key]=true
				if err:=walk(depth+1);err!=nil{return err}
			}
			end,err:=decoder.Token()
			if err!=nil||end!=json.Delim('}'){return errors.New("bundle JSON object is incomplete")}
		case '[':
			for decoder.More(){if err:=walk(depth+1);err!=nil{return err}}
			end,err:=decoder.Token()
			if err!=nil||end!=json.Delim(']'){return errors.New("bundle JSON array is incomplete")}
		default:return errors.New("unexpected bundle JSON delimiter")
		}
		return nil
	}
	if err:=walk(0);err!=nil{return err}
	if _,err:=decoder.Token();err!=io.EOF{return errors.New("bundle artifact must contain one JSON value")}
	return nil
}
