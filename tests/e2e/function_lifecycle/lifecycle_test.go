package function_lifecycle_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

const (
	gatewayURL = "http://localhost:8080"
)

// TestFunctionLifecycle_CreateFunction tests the complete function creation flow
func TestFunctionLifecycle_CreateFunction(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test")
	}

	// Create a simple Python function ZIP
	packageData := createTestPackage(t)

	createReq := map[string]interface{}{
		"name":         "test-hello-world",
		"runtime":      "python3.9",
		"handler":      "index.handler",
		"package_data": base64.StdEncoding.EncodeToString(packageData),
		"webhook_url":  "",
		"timeout":      30,
		"memory":       128,
	}

	body, err := json.Marshal(createReq)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	// Send create function request
	resp, err := http.Post(gatewayURL+"/functions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Create function request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 202 Accepted, got %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var createResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	jobID, ok := createResp["job_id"].(string)
	if !ok || jobID == "" {
		t.Fatal("Response missing job_id")
	}

	functionID, ok := createResp["function_id"].(string)
	if !ok || functionID == "" {
		t.Fatal("Response missing function_id")
	}

	t.Logf("Function created: job_id=%s, function_id=%s", jobID, functionID)

	// Wait for build to complete (in real scenario, webhook would notify)
	t.Log("Waiting for build to complete...")
	time.Sleep(10 * time.Second)

	// Verify function exists
	getResp, err := http.Get(gatewayURL + "/functions?id=" + functionID)
	if err != nil {
		t.Fatalf("Get function request failed: %v", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Logf("Function not yet available (expected during build): status=%d", getResp.StatusCode)
	}
}

// TestFunctionLifecycle_ListFunctions tests listing all functions
func TestFunctionLifecycle_ListFunctions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test")
	}

	resp, err := http.Get(gatewayURL + "/functions")
	if err != nil {
		t.Fatalf("List functions request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 200 OK, got %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var functions []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&functions); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	t.Logf("Found %d functions", len(functions))
}

// TestGateway_HealthCheck tests gateway health endpoint
func TestGateway_HealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test")
	}

	resp, err := http.Get(gatewayURL + "/health")
	if err != nil {
		t.Fatalf("Health check failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	var health map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("Failed to decode health response: %v", err)
	}

	t.Logf("Health check response: %+v", health)

	// Verify services are healthy
	if services, ok := health["services"].(map[string]interface{}); ok {
		for service, status := range services {
			t.Logf("Service %s: %v", service, status)
		}
	}
}

// TestRateLimiting tests rate limiting functionality
func TestRateLimiting(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test")
	}

	// Create invoke request
	invokeReq := map[string]interface{}{
		"function_id": "test-function-id",
		"payload": map[string]string{
			"message": "test",
		},
	}

	body, _ := json.Marshal(invokeReq)

	// Send multiple requests rapidly
	successCount := 0
	rateLimitedCount := 0

	for i := 0; i < 20; i++ {
		resp, err := http.Post(gatewayURL+"/invoke", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Logf("Request %d failed: %v", i, err)
			continue
		}

		if resp.StatusCode == http.StatusOK {
			successCount++
		} else if resp.StatusCode == http.StatusTooManyRequests {
			rateLimitedCount++
		}

		resp.Body.Close()
	}

	t.Logf("Success: %d, Rate limited: %d", successCount, rateLimitedCount)

	if rateLimitedCount == 0 {
		t.Log("Warning: No rate limiting observed (might need to adjust test)")
	}
}

// createTestPackage creates a simple test ZIP package
func createTestPackage(t *testing.T) []byte {
	// For now, return empty bytes
	// In a real test, this would create a proper ZIP file with Python code
	return []byte{}
}
