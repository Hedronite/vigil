package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func assertVigilCode(t *testing.T, body, code string) {
	t.Helper()
	var doc struct {
		APIVersion string `json:"apiVersion"`
		Error      struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &doc); err != nil {
		t.Fatalf("json: %v body=%q", err, body)
	}
	if doc.APIVersion != "vigil.error/v0" {
		t.Fatalf("apiVersion = %q", doc.APIVersion)
	}
	if doc.Error.Code != code {
		t.Fatalf("code = %q, want %q body=%q", doc.Error.Code, code, body)
	}
}
