package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	// "log"
	"net/http"

	"github.gatech.edu/faasedge/fecore/pkg/timec"
)

type MetricsExport struct {
	ColdStart      []int64
	InvocationCold []int64
	InvocationWarm []int64
}

// MakeMetricsHandler creates exposes function/node metrics
func MakeMetricsHandler(fs *FunctionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			defer r.Body.Close()
		}

		// metrics := &MetricsExport{
		// 		ColdStart: fs.GetColdStartTimes(),
		// 		InvocationCold: fs.GetInvocationTime(true),
		// 		InvocationWarm: fs.GetInvocationTime(false),
		// 	}
		// // log.Println("HERE", metrics)
		// jsonOut, marshalErr := json.Marshal(metrics)

		switch action := r.Header.Get("action"); action {
		case "flush":
			timec.ClearDurationLog()
		case "write":
			timec.WriteDurationLog()
		case "metrics":
			WriteMetricsLog(fs)
		}

		timec.WriteDurationLog()

		jsonOut, marshalErr := json.Marshal(timec.TimeLog)
		if marshalErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(jsonOut)
	}
}

func WriteMetricsLog(fs *FunctionStore) {
	filename := fmt.Sprintf("/mnt/faasedge/logs/metrics-%d.log", time.Now().UnixMilli())
	f, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Error opening log file: %v", err)
	}
	defer f.Close()
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetOutput(f)
	for fname, fstat := range fs.functionStats {
		for i := 0; i < fstat.startupTimesPos; i++ {
			log.Printf("%s, %d, %d\n", fname, fstat.startupTimes[i], fstat.execTimes[i])
		}
		log.Printf("---\n")
	}
}
