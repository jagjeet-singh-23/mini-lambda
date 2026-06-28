package executor

import (
	"strings"
	"testing"

	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
)

func TestBuildExecutionCommand_ReadsFromDisk_Python(t *testing.T) {
	r := &DockerRuntime{runtimeType: "python3.9"}
	fn := &domain.Function{Code: []byte("functions/fn-123/v1")} // S3 key, not code
	cmd := r.buildExecutionCommand(fn, []byte(`{"key":"value"}`))

	full := strings.Join(cmd, " ")
	// Must reference handler on disk
	if !strings.Contains(full, "/tmp/handler.py") {
		t.Errorf("exec command must reference /tmp/handler.py, got: %s", full)
	}
	// Must NOT contain the S3 key (code bytes should not appear in the exec command)
	if strings.Contains(full, "functions/fn-123/v1") {
		t.Errorf("exec command must not embed the S3 key / code bytes")
	}
}

func TestBuildExecutionCommand_ReadsFromDisk_Node(t *testing.T) {
	r := &DockerRuntime{runtimeType: "nodejs18"}
	fn := &domain.Function{Code: []byte("functions/fn-123/v1")}
	cmd := r.buildExecutionCommand(fn, []byte(`{"key":"value"}`))

	full := strings.Join(cmd, " ")
	if !strings.Contains(full, "/tmp/handler.js") {
		t.Errorf("exec command must reference /tmp/handler.js, got: %s", full)
	}
	if strings.Contains(full, "functions/fn-123/v1") {
		t.Errorf("exec command must not embed the S3 key / code bytes")
	}
}
