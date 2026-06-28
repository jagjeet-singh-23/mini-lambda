package executor

import (
	"testing"
)

func TestHandlerFilename(t *testing.T) {
	cases := []struct {
		runtime string
		want    string
	}{
		{"python3.9", "handler.py"},
		{"python3.11", "handler.py"},
		{"nodejs18", "handler.js"},
		{"nodejs20", "handler.js"},
		{"go1.21", "handler.go"},
		{"unknown", "handler.sh"},
	}
	for _, tc := range cases {
		got := handlerFilename(tc.runtime)
		if got != tc.want {
			t.Errorf("handlerFilename(%q) = %q, want %q", tc.runtime, got, tc.want)
		}
	}
}

func TestBaseImageForRuntime(t *testing.T) {
	cases := []struct {
		runtime string
		want    string
	}{
		{"python3.9", "python:3.9-slim"},
		{"python3.11", "python:3.11-slim"},
		{"nodejs18", "node:18-slim"},
		{"nodejs20", "node:20-slim"},
		{"go1.21", "golang:1.21-alpine"},
		{"unknown", "alpine"},
	}
	for _, tc := range cases {
		got := baseImageForRuntime(tc.runtime)
		if got != tc.want {
			t.Errorf("baseImageForRuntime(%q) = %q, want %q", tc.runtime, got, tc.want)
		}
	}
}
