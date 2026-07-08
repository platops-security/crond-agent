package webhook

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
)

const admissionAPIVersion = "admission.k8s.io/v1"

// Handler serves AdmissionReview requests for CronJob CREATE/UPDATE and returns
// a JSON-Patch mutation. It NEVER denies admission — an observability add-on
// must not block a user's workloads — so every well-formed request is allowed,
// with or without a patch. Pair with failurePolicy: Ignore so webhook outages
// are equally non-blocking.
type Handler struct {
	cfg    InjectorConfig
	logger *slog.Logger
}

// NewHandler constructs a Handler with the injection template config.
func NewHandler(cfg InjectorConfig, logger *slog.Logger) *Handler {
	return &Handler{cfg: cfg, logger: logger}
}

// minimal AdmissionReview envelope (admission.k8s.io/v1).
type admissionReview struct {
	APIVersion string             `json:"apiVersion"`
	Kind       string             `json:"kind"`
	Request    *admissionRequest  `json:"request,omitempty"`
	Response   *admissionResponse `json:"response,omitempty"`
}

type admissionRequest struct {
	UID    string          `json:"uid"`
	Object json.RawMessage `json:"object"`
}

type admissionResponse struct {
	UID       string   `json:"uid"`
	Allowed   bool     `json:"allowed"`
	PatchType *string  `json:"patchType,omitempty"`
	Patch     []byte   `json:"patch,omitempty"` // marshals to base64 — what the API expects
	Warnings  []string `json:"warnings,omitempty"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 3<<20)) // 3 MiB cap
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	var review admissionReview
	if err := json.Unmarshal(body, &review); err != nil || review.Request == nil {
		// Can't extract a UID to answer against — let the apiserver's
		// failurePolicy (Ignore) decide. A 400 is treated as a webhook error.
		http.Error(w, "invalid AdmissionReview", http.StatusBadRequest)
		return
	}

	resp := h.review(review.Request)

	out := admissionReview{
		APIVersion: admissionAPIVersion,
		Kind:       "AdmissionReview",
		Response:   resp,
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		h.logger.Error("encode admission response", "error", err)
	}
}

// review runs the injection decision and maps it to an AdmissionResponse.
// Any decision error is logged and downgraded to a plain allow — never a deny.
func (h *Handler) review(req *admissionRequest) *admissionResponse {
	resp := &admissionResponse{UID: req.UID, Allowed: true}

	result, err := BuildInjection(req.Object, h.cfg)
	if err != nil {
		h.logger.Warn("injection decode failed; admitting unmodified", "uid", req.UID, "error", err)
		return resp
	}
	if result.Warning != "" {
		h.logger.Info("injection skipped", "uid", req.UID, "reason", result.Warning)
		resp.Warnings = []string{result.Warning}
	}
	if len(result.Patch) == 0 {
		return resp
	}

	patchBytes, err := json.Marshal(result.Patch)
	if err != nil {
		h.logger.Error("marshal patch; admitting unmodified", "uid", req.UID, "error", err)
		return resp
	}
	jsonPatch := "JSONPatch"
	resp.PatchType = &jsonPatch
	resp.Patch = patchBytes
	h.logger.Info("injected crond-agent wrapper", "uid", req.UID, "ops", len(result.Patch))
	return resp
}
