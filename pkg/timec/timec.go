package timec

import "time"

type TimeTrack struct {
	Fn       string
	Duration int64
}

var TimeLog []TimeTrack

func RecordDuration(fn string, start time.Time) {
	duration := time.Since(start)
	event := TimeTrack{}
	event.Fn = fn
	event.Duration = duration.Microseconds()
	// fmt.
	TimeLog = append(TimeLog, event)
}