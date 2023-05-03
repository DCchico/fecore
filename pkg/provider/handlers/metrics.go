package handlers

import (
	"encoding/json"
	// "log"
	"net/http"
)

type MetricsExport struct {
    ColdStart       []int64
    InvocationCold  []int64
	InvocationWarm  []int64
}

//MakeMetricsHandler creates exposes function/node metrics
func MakeMetricsHandler(fs *FunctionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			defer r.Body.Close()
		}

		metrics := &MetricsExport{
			ColdStart: fs.GetColdStartTimes(),
			InvocationCold: fs.GetInvocationTime(true),
			InvocationWarm: fs.GetInvocationTime(false),
		}
		// log.Println("HERE", metrics)
		jsonOut, marshalErr := json.Marshal(metrics)
		if marshalErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(jsonOut)
	}
}
