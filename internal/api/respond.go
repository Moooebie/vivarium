package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"vivarium/internal/auth"
	"vivarium/internal/keyring"
	"vivarium/internal/models"
	"vivarium/internal/store"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// writeMappedError translates domain errors into HTTP status codes.
func writeMappedError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrConflict), errors.Is(err, keyring.ErrAlreadyExists):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, keyring.ErrLocked):
		writeError(w, http.StatusLocked, err.Error())
	case errors.Is(err, keyring.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, auth.ErrUnconfigured):
		writeError(w, http.StatusPreconditionRequired, err.Error())
	default:
		var vErr models.ValidationError
		if errors.As(err, &vErr) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}
