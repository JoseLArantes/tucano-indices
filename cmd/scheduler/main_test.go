package main

import (
	"testing"
	"time"
)

func TestNextWeekdayAfter(t *testing.T) {
	location, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		now  time.Time
		job  schedule
		want string
	}{
		{name: "before scheduled time", now: time.Date(2026, time.November, 16, 6, 0, 0, 0, location), job: schedule{source: "focus", hour: 7}, want: "2026-11-16 07:00"},
		{name: "after scheduled time", now: time.Date(2026, time.November, 16, 7, 1, 0, 0, location), job: schedule{source: "focus", hour: 7}, want: "2026-11-17 07:00"},
		{name: "weekend skips to monday", now: time.Date(2026, time.November, 21, 6, 0, 0, 0, location), job: schedule{source: "focus", hour: 7}, want: "2026-11-23 07:00"},
		{name: "friday after run skips weekend", now: time.Date(2026, time.November, 20, 22, 0, 0, 0, location), job: schedule{source: "b3", hour: 21}, want: "2026-11-23 21:00"},
		{name: "minute is retained", now: time.Date(2026, time.November, 16, 6, 0, 0, 0, location), job: schedule{source: "tesouro", hour: 18, minute: 30}, want: "2026-11-16 18:30"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := nextWeekdayAfter(test.now, test.job).In(location).Format("2006-01-02 15:04")
			if got != test.want {
				t.Fatalf("nextWeekdayAfter() = %q, want %q", got, test.want)
			}
		})
	}
}
