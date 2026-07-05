// Package testsupport holds helpers shared across the tests/e2e test
// packages, so setup logic (building a test package, waiting for the stack
// to be ready) isn't duplicated per package.
package testsupport

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"
)

// BuildZip builds a valid ZIP archive in memory from a map of relative file
// path to file content, suitable for use as CreateFunctionRequest.PackageData.
func BuildZip(files map[string]string) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			panic(fmt.Sprintf("testsupport.BuildZip: create %q: %v", name, err))
		}
		if _, err := f.Write([]byte(content)); err != nil {
			panic(fmt.Sprintf("testsupport.BuildZip: write %q: %v", name, err))
		}
	}
	if err := w.Close(); err != nil {
		panic(fmt.Sprintf("testsupport.BuildZip: close: %v", err))
	}
	return buf.Bytes()
}

// WaitForGatewayHealthy polls the gateway's /health endpoint until it
// responds 200 or ctx is done, returning a clear error in the latter case
// instead of letting every test in the package fail with a confusing
// connection-refused error.
func WaitForGatewayHealthy(ctx context.Context, gatewayURL string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, gatewayURL+"/health", nil)
		if err == nil {
			if resp, err := client.Do(req); err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("gateway at %s did not become healthy: %w", gatewayURL, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}
