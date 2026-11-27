package server

import (
	"encoding/json"
	"net/http"
)

// UnauthorizedBody is the byte-identical 401 for a missing or wrong credential.
const UnauthorizedBody = `{"apiVersion":"vigil.error/v0","error":{"code":"unauthorized"}}` + "\n"

const healthBody = `{"status":"ok"}`

type apiError struct {
	APIVersion string       `json:"apiVersion"`
	Error      apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

func writeAPI(w http.ResponseWriter, status int, code string) {
	doc := apiError{APIVersion: "vigil.error/v0", Error: apiErrorBody{Code: code}}
	b, err := json.Marshal(doc)
	if err != nil {
		b = []byte(`{"apiVersion":"vigil.error/v0","error":{"code":"internal"}}`)
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeBody(w, append(b, '\n'))
}
