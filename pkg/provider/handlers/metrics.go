package handlers

import (
	"encoding/json"
	"net/http"
)

//MakeMetricsHandler creates exposes function/node metrics
func MakeMetricsHandler(fs *FunctionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			defer r.Body.Close()
		}

		jsonOut, marshalErr := json.Marshal(fs.GetColdStartTimes())
		if marshalErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(jsonOut)
	}
}
