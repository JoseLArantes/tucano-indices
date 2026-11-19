package scheduler

import "time"

// Job describes a recurring weekday sync.
type Job struct {
	Source string
	Hour   int
	Minute int
}

// Next returns the next weekday occurrence of the job after now.
func (j Job) Next(now time.Time) time.Time {
	return NextWeekdayAfter(now, j)
}

// NextWeekdayAfter returns the next weekday occurrence of job after now in
// America/Sao_Paulo, falling back to a fixed BRT offset if the zone is
// unavailable.
func NextWeekdayAfter(now time.Time, job Job) time.Time {
	location, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		location = time.FixedZone("BRT", -3*60*60)
	}
	now = now.In(location)
	next := time.Date(now.Year(), now.Month(), now.Day(), job.Hour, job.Minute, 0, 0, location)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	for next.Weekday() == time.Saturday || next.Weekday() == time.Sunday {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
