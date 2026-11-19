package domain

import (
	"encoding/json"
	"time"
)

type Series struct {
	Code        string    `json:"code"`
	Group       int16     `json:"group"`
	Name        string    `json:"name"`
	Unit        string    `json:"unit"`
	Frequency   string    `json:"frequency"`
	Source      string    `json:"source"`
	Dimension   *string   `json:"dimension,omitempty"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ObservationInput struct {
	Dimension  string          `json:"dimension,omitempty"`
	Date       string          `json:"date"`
	Value      string          `json:"value"`
	Attributes json.RawMessage `json:"attributes,omitempty"`
	Source     string          `json:"source,omitempty"`
}

type Observation struct {
	SeriesCode string          `json:"series_code"`
	Dimension  string          `json:"dimension,omitempty"`
	Date       string          `json:"date"`
	Value      string          `json:"value"`
	Revision   int32           `json:"revision"`
	IsCurrent  bool            `json:"is_current"`
	Source     string          `json:"source"`
	RecordedAt time.Time       `json:"recorded_at"`
	Attributes json.RawMessage `json:"attributes,omitempty"`
}

type ChartPoint struct {
	X          string          `json:"x"`
	Y          string          `json:"y"`
	Dimension  string          `json:"dimension,omitempty"`
	Source     string          `json:"source,omitempty"`
	Attributes json.RawMessage `json:"attributes,omitempty"`
}

type SyncRun struct {
	ID           string     `json:"id"`
	Source       string     `json:"source"`
	Status       string     `json:"status"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	FromDate     *string    `json:"from,omitempty"`
	ToDate       *string    `json:"to,omitempty"`
	PointsSeen   int        `json:"points_seen"`
	PointsStored int        `json:"points_stored"`
	Error        *string    `json:"error,omitempty"`
}

type SeriesList struct {
	Items []Series `json:"items"`
}

type SeriesResponse struct {
	Series   Series       `json:"series"`
	Data     []ChartPoint `json:"data"`
	Warnings []string     `json:"warnings"`
}

type FinalResponse struct {
	Series   Series       `json:"series"`
	Data     []ChartPoint `json:"data"`
	Warnings []string     `json:"warnings"`
}
