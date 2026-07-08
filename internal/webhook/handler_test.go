package webhook

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestHandler() *Handler {
	return NewHandler(testConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// admissionReviewJSON wraps a CronJob object in an AdmissionReview request.
func admissionReviewJSON(t *testing.T, uid string, cronjob []byte) []byte {
	t.Helper()
	review := map[string]any{
		"apiVersion": "admission.k8s.io/v1",
		"kind":       "AdmissionReview",
		"request": map[string]any{
			"uid":    uid,
			"object": json.RawMessage(cronjob),
		},
	}
	b, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func postReview(t *testing.T, h *Handler, body []byte) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mutate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

func TestHandler_InjectsOptedInCronJob(t *testing.T) {
	cj := cronJobJSON(t, map[string]string{
		"crond.io/inject":       "true",
		"crond.io/ping-key-env": "PING_KEY_BACKUP",
	}, map[string]any{
		"name": "backup", "image": "myco/backup:1.0", "command": []any{"/opt/backup.sh"},
	})
	out := postReview(t, newTestHandler(), admissionReviewJSON(t, "uid-123", cj))

	resp, _ := out["response"].(map[string]any)
	if resp == nil {
		t.Fatalf("no response object: %v", out)
	}
	if resp["uid"] != "uid-123" {
		t.Fatalf("uid mismatch: %v", resp["uid"])
	}
	if resp["allowed"] != true {
		t.Fatalf("expected allowed=true, got %v", resp["allowed"])
	}
	if resp["patchType"] != "JSONPatch" {
		t.Fatalf("expected patchType JSONPatch, got %v", resp["patchType"])
	}
	// patch is base64-encoded JSON — decode and confirm it carries the rewrite.
	patchB64, _ := resp["patch"].(string)
	raw, err := base64.StdEncoding.DecodeString(patchB64)
	if err != nil {
		t.Fatalf("patch not base64: %v", err)
	}
	var ops []PatchOp
	if err := json.Unmarshal(raw, &ops); err != nil {
		t.Fatalf("patch not a JSON patch: %v", err)
	}
	if findOp(ops, "replace", base+"/containers/0/command") == nil {
		t.Fatalf("patch missing command rewrite: %s", raw)
	}
}

func TestHandler_AllowsNonOptedInWithoutPatch(t *testing.T) {
	cj := cronJobJSON(t, map[string]string{}, map[string]any{
		"name": "backup", "command": []any{"/opt/backup.sh"},
	})
	out := postReview(t, newTestHandler(), admissionReviewJSON(t, "uid-9", cj))
	resp := out["response"].(map[string]any)
	if resp["allowed"] != true {
		t.Fatal("must always allow")
	}
	if _, hasPatch := resp["patch"]; hasPatch {
		t.Fatalf("non-opted-in CronJob should get no patch: %v", resp)
	}
}

func TestHandler_SurfacesWarningOnSkip(t *testing.T) {
	// opted in but missing ping-key-env → allowed, no patch, warning surfaced.
	cj := cronJobJSON(t, map[string]string{"crond.io/inject": "true"}, map[string]any{
		"name": "backup", "command": []any{"/opt/backup.sh"},
	})
	out := postReview(t, newTestHandler(), admissionReviewJSON(t, "uid-w", cj))
	resp := out["response"].(map[string]any)
	if resp["allowed"] != true {
		t.Fatal("must allow even when skipping")
	}
	if _, hasPatch := resp["patch"]; hasPatch {
		t.Fatal("skip should produce no patch")
	}
	warnings, _ := resp["warnings"].([]any)
	if len(warnings) == 0 {
		t.Fatalf("expected a warning, got %v", resp)
	}
}

func TestHandler_RejectsMalformedBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/mutate", bytes.NewReader([]byte("{not json")))
	rec := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed body should be 400, got %d", rec.Code)
	}
}
