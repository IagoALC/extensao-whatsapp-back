package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/iago/extensao-whatsapp-back/internal/repository"
)

func (api *API) JobStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	jobID := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "job_id is required")
		return
	}

	job, err := api.jobsService.GetJob(r.Context(), jobID)
	if err != nil {
		if err == repository.ErrNotFound {
			writeError(w, r, http.StatusNotFound, "not_found", "job not found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to load job")
		return
	}

	response := map[string]any{
		"job_id":     job.ID,
		"status":     job.Status,
		"kind":       job.Kind,
		"updated_at": job.UpdatedAt,
	}
	if len(job.Result) > 0 {
		response["result"] = jsonRawOrFallback(job.Result)
	}
	if strings.TrimSpace(job.ErrorMessage) != "" {
		response["error"] = map[string]any{
			"code":    "processing_error",
			"message": job.ErrorMessage,
		}
	}

	writeJSON(w, http.StatusOK, response)
}

func jsonRawOrFallback(value []byte) any {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err == nil {
		return decoded
	}
	return string(value)
}
