package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

type APIError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return e.Message }

func Err(status int, code, msg string) *APIError {
	return &APIError{Status: status, Code: code, Message: msg}
}

var (
	ErrUnauthorized = Err(http.StatusUnauthorized, "unauthorized", "unauthorized")
	ErrForbidden    = Err(http.StatusForbidden, "forbidden", "forbidden")
	ErrNotFound     = Err(http.StatusNotFound, "not_found", "not found")
	ErrBadRequest   = Err(http.StatusBadRequest, "bad_request", "bad request")
	ErrConflict     = Err(http.StatusConflict, "conflict", "conflict")
	ErrInternal     = Err(http.StatusInternalServerError, "internal", "internal error")
)

func JSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("json encode", "err", err)
	}
}

func WriteErr(w http.ResponseWriter, err error) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		JSON(w, apiErr.Status, apiErr)
		return
	}
	slog.Error("unhandled error", "err", err)
	JSON(w, http.StatusInternalServerError, ErrInternal)
}

func Decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return Err(http.StatusBadRequest, "invalid_body", err.Error())
	}
	return nil
}
