package server

import "net/http"

const writeFailedHeader = "X-Vigil-Write-Failed"

func writeBody(w http.ResponseWriter, b []byte) {
	if _, err := w.Write(b); err != nil {
		w.Header().Set(writeFailedHeader, "1")
		return
	}
	w.Header().Set(writeFailedHeader, "0")
}
