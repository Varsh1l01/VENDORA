package health

import (
	"encoding/json"
	"net/http"
)

type Response struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

// Handler returns a health check HTTP handler
func Handler(serviceName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(Response{
			Status:  "ok",
			Service: serviceName,
		})
	}
}
