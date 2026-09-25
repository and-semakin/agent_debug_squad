package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/orchestrator"
)

func (s *Server) handlePermissionReply(w http.ResponseWriter, r *http.Request) {
	if s.rejectOneShotForeignRun(w, r.PathValue("run_id")) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRunRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var reply domain.PermissionReply
	err := decoder.Decode(&reply)
	if err == nil {
		var extra any
		if trailingErr := decoder.Decode(&extra); trailingErr != io.EOF {
			if trailingErr != nil {
				err = trailingErr
			} else {
				err = errors.New("expected one JSON object")
			}
		}
	}
	if err == nil {
		err = reply.Validate()
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, err)
		} else {
			writeError(w, http.StatusBadRequest, err)
		}
		return
	}
	err = s.orchestrator.ReplyPermission(r.Context(), r.PathValue("run_id"), r.PathValue("request_id"), reply)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case errors.Is(err, orchestrator.ErrRunNotFound), errors.Is(err, domain.ErrPermissionNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, domain.ErrPermissionInactive), errors.Is(err, domain.ErrPermissionUnsupported):
		writeError(w, http.StatusConflict, err)
	case errors.Is(err, domain.ErrPermissionReplyInvalid), isUnsafePathError(err):
		writeError(w, http.StatusBadRequest, err)
	default:
		writeError(w, http.StatusBadGateway, err)
	}
}
