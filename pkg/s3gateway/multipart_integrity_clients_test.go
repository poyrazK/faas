package s3gateway

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1038
func TestMultipartIntegrityStockClients(t *testing.T) {
	for _, language := range []string{"node", "boto3", "awscli"} {
		t.Run(language, func(t *testing.T) {
			nodeModules := os.Getenv("GREGALE_S3_CHECKSUM_NODE_MODULES")
			python := os.Getenv("GREGALE_S3_CHECKSUM_PYTHON")
			cli := os.Getenv("GREGALE_S3_CHECKSUM_AWSCLI")
			if language == "node" && nodeModules == "" || language == "boto3" && python == "" || language == "awscli" && cli == "" {
				if os.Getenv("GREGALE_S3_CHECKSUM_INTEROP_REQUIRED") == "1" {
					t.Fatal("required stock client environment missing")
				}
				t.Skip("stock client environment not configured")
			}
			native := &integrityNativeFixture{}
			f := newMultipartCopyIntegrationWithTransfer(t, state.NewMemStore(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { native.serve(t, w, r) }), transferIntegrationConfig(3600), 0)
			endpoint := aws.ToString(f.client.Options().BaseEndpoint)
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			var cmd *exec.Cmd
			switch language {
			case "node":
				cmd = exec.CommandContext(ctx, "node", filepath.Join("testdata", "multipart-integrity.cjs"), nodeModules, endpoint, testAccess, testSecret)
			case "boto3":
				cmd = exec.CommandContext(ctx, python, filepath.Join("testdata", "multipart-integrity.py"), endpoint, testAccess, testSecret)
			case "awscli":
				dir := t.TempDir()
				source := filepath.Join(dir, "source")
				if err := os.WriteFile(source, append(bytes.Repeat([]byte("a"), 5<<20), []byte("last part bytes")...), 0600); err != nil {
					t.Fatal(err)
				}
				config := filepath.Join(dir, "config")
				if err := os.WriteFile(config, []byte("[default]\nregion = us-east-1\ns3 =\n    addressing_style = path\n    multipart_threshold = 5MB\n    multipart_chunksize = 5MB\n"), 0600); err != nil {
					t.Fatal(err)
				}
				cmd = exec.CommandContext(ctx, cli, "s3", "cp", source, "s3://assets/integrity", "--endpoint-url", endpoint, "--no-progress")
				for _, item := range os.Environ() {
					key, _, _ := strings.Cut(item, "=")
					switch key {
					case "AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_SESSION_TOKEN", "AWS_SECURITY_TOKEN":
						continue
					}
					cmd.Env = append(cmd.Env, item)
				}
				cmd.Env = append(cmd.Env, "AWS_CONFIG_FILE="+config, "AWS_SHARED_CREDENTIALS_FILE="+filepath.Join(dir, "no-credentials"), "AWS_ACCESS_KEY_ID="+testAccess, "AWS_SECRET_ACCESS_KEY="+testSecret, "AWS_SESSION_TOKEN=", "AWS_EC2_METADATA_DISABLED=true", "AWS_MAX_ATTEMPTS=1")
			}
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", language, err, output)
			}
			t.Log(string(output))
			if native.completions != 1 || !native.result.Complete() {
				t.Fatal("stock client did not complete with verified checksum", native.completions, native.result)
			}
			if language == "awscli" {
				read, err := f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String("integrity")})
				if err != nil {
					t.Fatal(err)
				}
				_ = read.Body.Close()
			}
		})
	}
}
