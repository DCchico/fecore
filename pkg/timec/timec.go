package timec

import (
	"fmt"
	"log"
	"os"
	"time"
)

type TimeTrack struct {
	Fn       string
	Duration int64
}

var TimeLog []TimeTrack

func ClearDurationLog() {
	TimeLog = TimeLog[:0]
}

func WriteDurationLog() {
	filename := fmt.Sprintf("/tmp/timec-%d.log", time.Now().UnixMilli())
	f, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Error opening log file: %v", err)
	}
	defer f.Close()
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetOutput(f)
	for _, v := range TimeLog {
		log.Printf("%s, %d\n", v.Fn, v.Duration)
	}
}

func RecordDuration(fn string, start time.Time) {
	duration := time.Since(start)
	event := TimeTrack{}
	event.Fn = fn
	event.Duration = duration.Microseconds()
	// fmt.
	TimeLog = append(TimeLog, event)
}
