package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JoseLArantes/tucano-indices/internal/domain"
)

type queryPool interface {
	Ping(context.Context) error
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	BeginTx(context.Context, pgx.TxOptions) (transaction, error)
}

type transaction interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Commit(context.Context) error
	Rollback(context.Context) error
}

type sourceConnection interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Release()
}

type Postgres struct {
	pool    queryPool
	acquire func(context.Context) (sourceConnection, error)
}

func New(pool *pgxpool.Pool) *Postgres {
	return &Postgres{
		pool: pgxPoolAdapter{pool: pool},
		acquire: func(ctx context.Context) (sourceConnection, error) {
			return pool.Acquire(ctx)
		},
	}
}

type pgxPoolAdapter struct{ pool *pgxpool.Pool }

func (p pgxPoolAdapter) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }
func (p pgxPoolAdapter) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return p.pool.Query(ctx, sql, args...)
}
func (p pgxPoolAdapter) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return p.pool.QueryRow(ctx, sql, args...)
}
func (p pgxPoolAdapter) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return p.pool.Exec(ctx, sql, args...)
}
func (p pgxPoolAdapter) BeginTx(ctx context.Context, options pgx.TxOptions) (transaction, error) {
	tx, err := p.pool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return pgxTransactionAdapter{tx: tx}, nil
}

type pgxTransactionAdapter struct{ tx pgx.Tx }

func (t pgxTransactionAdapter) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return t.tx.QueryRow(ctx, sql, args...)
}
func (t pgxTransactionAdapter) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.tx.Exec(ctx, sql, args...)
}
func (t pgxTransactionAdapter) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t pgxTransactionAdapter) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }

func (s *Postgres) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Postgres) ListSeries(ctx context.Context, group int, codes []string) ([]domain.Series, error) {
	query := `SELECT code, grp, name, unit, frequency, source, dimension, description, created_at, updated_at
		FROM index_series WHERE ($1 = 0 OR grp = $1) AND (cardinality($2::text[]) = 0 OR code = ANY($2::text[])) ORDER BY grp, code`
	rows, err := s.pool.Query(ctx, query, group, codes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Series, 0)
	for rows.Next() {
		item, err := scanSeries(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Postgres) GetSeries(ctx context.Context, code string) (domain.Series, error) {
	row := s.pool.QueryRow(ctx, `SELECT code, grp, name, unit, frequency, source, dimension, description, created_at, updated_at
		FROM index_series WHERE code = $1`, strings.ToLower(strings.TrimSpace(code)))
	return scanSeries(row)
}

func (s *Postgres) Latest(ctx context.Context, code, dimension string) ([]domain.Observation, error) {
	query := `SELECT series_code, dimension, ref_date::text, value::text, revision, is_current, source, recorded_at, attributes
		FROM (SELECT DISTINCT ON (dimension) series_code, dimension, ref_date, value, revision, is_current, source, recorded_at, attributes
		      FROM index_observations WHERE series_code = $1 AND is_current AND ($2 = '' OR dimension = $2)
		      ORDER BY dimension, ref_date DESC) latest ORDER BY dimension`
	rows, err := s.pool.Query(ctx, query, strings.ToLower(strings.TrimSpace(code)), dimension)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanObservations(rows)
}

func (s *Postgres) LatestAsOf(ctx context.Context, code, dimension, asOf string) ([]domain.Observation, error) {
	query := `SELECT series_code, dimension, ref_date::text, value::text, revision, is_current, source, recorded_at, attributes
		FROM (SELECT DISTINCT ON (dimension) series_code, dimension, ref_date, value, revision, is_current, source, recorded_at, attributes
		      FROM index_observations WHERE series_code = $1 AND is_current AND ($2 = '' OR dimension = $2) AND ref_date <= $3::date
		      ORDER BY dimension, ref_date DESC) latest ORDER BY dimension`
	rows, err := s.pool.Query(ctx, query, strings.ToLower(strings.TrimSpace(code)), dimension, asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanObservations(rows)
}

type SeriesQuery struct {
	From       string
	To         string
	AsOf       string
	Dimension  string
	Limit      int
	Offset     int
	Resolution string
}

func (s *Postgres) Series(ctx context.Context, code string, q SeriesQuery) ([]domain.ChartPoint, error) {
	if q.Limit <= 0 || q.Limit > 20000 {
		q.Limit = 2000
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	if q.Resolution == "" {
		q.Resolution = "raw"
	}
	if q.Resolution != "raw" && q.Resolution != "weekly" && q.Resolution != "monthly" {
		return nil, fmt.Errorf("resolution must be raw, weekly, or monthly")
	}
	var query string
	if q.Resolution == "raw" {
		query = `SELECT series_code, dimension, ref_date::text, value::text, revision, is_current, source, recorded_at, attributes
			FROM index_observations WHERE series_code = $1 AND is_current
			AND ($2 = '' OR dimension = $2) AND ($3::date IS NULL OR ref_date >= $3::date)
			AND ($4::date IS NULL OR ref_date <= $4::date) AND ($5::date IS NULL OR ref_date <= $5::date)
			ORDER BY dimension, ref_date DESC LIMIT $6 OFFSET $7`
	} else {
		query = `SELECT series_code, dimension, ref_date::text, value::text, revision, is_current, source, recorded_at, attributes FROM (
			SELECT DISTINCT ON (dimension, date_trunc($6, ref_date)) series_code, dimension, ref_date, value, revision, is_current, source, recorded_at, attributes
			FROM index_observations WHERE series_code = $1 AND is_current
			AND ($2 = '' OR dimension = $2) AND ($3::date IS NULL OR ref_date >= $3::date)
			AND ($4::date IS NULL OR ref_date <= $4::date) AND ($5::date IS NULL OR ref_date <= $5::date)
			ORDER BY dimension, date_trunc($6, ref_date), ref_date DESC
		) sampled ORDER BY dimension, ref_date DESC LIMIT $7 OFFSET $8`
	}
	var rows pgx.Rows
	var err error
	if q.Resolution == "raw" {
		rows, err = s.pool.Query(ctx, query, strings.ToLower(strings.TrimSpace(code)), q.Dimension, nullableDate(q.From), nullableDate(q.To), nullableDate(q.AsOf), q.Limit, q.Offset)
	} else {
		unit := "week"
		if q.Resolution == "monthly" {
			unit = "month"
		}
		rows, err = s.pool.Query(ctx, query, strings.ToLower(strings.TrimSpace(code)), q.Dimension, nullableDate(q.From), nullableDate(q.To), nullableDate(q.AsOf), unit, q.Limit, q.Offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	observations, err := scanObservations(rows)
	if err != nil {
		return nil, err
	}
	points := make([]domain.ChartPoint, 0, len(observations))
	for _, item := range observations {
		points = append(points, domain.ChartPoint{X: item.Date, Y: item.Value, Dimension: item.Dimension, Source: item.Source, Attributes: item.Attributes})
	}
	sort.Slice(points, func(i, j int) bool {
		if points[i].Dimension == points[j].Dimension {
			return points[i].X < points[j].X
		}
		return points[i].Dimension < points[j].Dimension
	})
	return points, nil
}

func (s *Postgres) ObservationSeries(ctx context.Context, code string, q SeriesQuery) ([]domain.Observation, error) {
	if q.Limit <= 0 || q.Limit > 20000 {
		q.Limit = 2000
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	if q.Resolution == "" {
		q.Resolution = "raw"
	}
	if q.Resolution != "raw" && q.Resolution != "weekly" && q.Resolution != "monthly" {
		return nil, fmt.Errorf("resolution must be raw, weekly, or monthly")
	}
	query := `SELECT series_code, dimension, ref_date::text, value::text, revision, is_current, source, recorded_at, attributes
		FROM index_observations WHERE series_code = $1 AND is_current
		AND ($2 = '' OR dimension = $2) AND ($3::date IS NULL OR ref_date >= $3::date)
		AND ($4::date IS NULL OR ref_date <= $4::date) AND ($5::date IS NULL OR ref_date <= $5::date)
		ORDER BY dimension, ref_date DESC LIMIT $6 OFFSET $7`
	var rows pgx.Rows
	var err error
	if q.Resolution == "raw" {
		rows, err = s.pool.Query(ctx, query, strings.ToLower(strings.TrimSpace(code)), q.Dimension, nullableDate(q.From), nullableDate(q.To), nullableDate(q.AsOf), q.Limit, q.Offset)
	} else {
		unit := "week"
		if q.Resolution == "monthly" {
			unit = "month"
		}
		query = `SELECT series_code, dimension, ref_date::text, value::text, revision, is_current, source, recorded_at, attributes FROM (
			SELECT DISTINCT ON (dimension, date_trunc($6, ref_date)) series_code, dimension, ref_date, value, revision, is_current, source, recorded_at, attributes
			FROM index_observations WHERE series_code = $1 AND is_current
			AND ($2 = '' OR dimension = $2) AND ($3::date IS NULL OR ref_date >= $3::date)
			AND ($4::date IS NULL OR ref_date <= $4::date) AND ($5::date IS NULL OR ref_date <= $5::date)
			ORDER BY dimension, date_trunc($6, ref_date), ref_date DESC
			) sampled ORDER BY dimension, ref_date DESC LIMIT $7 OFFSET $8`
		rows, err = s.pool.Query(ctx, query, strings.ToLower(strings.TrimSpace(code)), q.Dimension, nullableDate(q.From), nullableDate(q.To), nullableDate(q.AsOf), unit, q.Limit, q.Offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := scanObservations(rows)
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Dimension == items[j].Dimension {
			return items[i].Date < items[j].Date
		}
		return items[i].Dimension < items[j].Dimension
	})
	return items, nil
}

func (s *Postgres) MaxDate(ctx context.Context, code, dimension string) (time.Time, error) {
	var date *time.Time
	err := s.pool.QueryRow(ctx, `SELECT max(ref_date) FROM index_observations WHERE series_code = $1 AND is_current AND ($2 = '' OR dimension = $2)`,
		strings.ToLower(strings.TrimSpace(code)), dimension).Scan(&date)
	if err != nil || date == nil {
		return time.Time{}, err
	}
	return *date, nil
}

func (s *Postgres) CountCurrent(ctx context.Context, code, from, to string) (int64, error) {
	var total int64
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM index_observations WHERE series_code = $1 AND is_current
		AND ($2::date IS NULL OR ref_date >= $2::date) AND ($3::date IS NULL OR ref_date <= $3::date)`,
		strings.ToLower(strings.TrimSpace(code)), nullableDate(from), nullableDate(to)).Scan(&total)
	return total, err
}

func (s *Postgres) Upsert(ctx context.Context, code string, inputs []domain.ObservationInput, defaultSource string) (int, error) {
	code = strings.ToLower(strings.TrimSpace(code))
	if len(inputs) == 0 {
		return 0, nil
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	stored := 0
	for _, item := range inputs {
		refDate, err := time.Parse("2006-01-02", strings.TrimSpace(item.Date))
		if err != nil {
			return stored, fmt.Errorf("invalid observation date %q", item.Date)
		}
		value := strings.TrimSpace(item.Value)
		if _, ok := new(big.Rat).SetString(value); !ok {
			return stored, fmt.Errorf("invalid numeric value %q", item.Value)
		}
		attributes := item.Attributes
		if len(attributes) == 0 || !json.Valid(attributes) {
			attributes = json.RawMessage(`{}`)
		}
		source := strings.TrimSpace(item.Source)
		if source == "" {
			source = defaultSource
		}
		var oldValue string
		var revision int32
		err = tx.QueryRow(ctx, `SELECT value::text, revision FROM index_observations
			WHERE series_code = $1 AND dimension = $2 AND ref_date = $3 AND is_current FOR UPDATE`, code, item.Dimension, refDate).Scan(&oldValue, &revision)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return stored, err
		}
		if err == nil && sameDecimal(oldValue, value) {
			if _, err := tx.Exec(ctx, `UPDATE index_observations SET source = $5, attributes = $6::jsonb
				WHERE series_code = $1 AND dimension = $2 AND ref_date = $3 AND revision = $4`, code, item.Dimension, refDate, revision, source, string(attributes)); err != nil {
				return stored, err
			}
			continue
		}
		if revision > 0 {
			if _, err := tx.Exec(ctx, `UPDATE index_observations SET is_current = FALSE WHERE series_code = $1 AND dimension = $2 AND ref_date = $3 AND is_current`, code, item.Dimension, refDate); err != nil {
				return stored, err
			}
			revision++
		} else {
			revision = 1
		}
		id, err := newUUID()
		if err != nil {
			return stored, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO index_observations (id, series_code, dimension, ref_date, value, revision, source, attributes)
			VALUES ($1, $2, $3, $4, $5::numeric, $6, $7, $8::jsonb)`, id, code, item.Dimension, refDate, value, revision, source, string(attributes)); err != nil {
			return stored, err
		}
		stored++
	}
	if err := tx.Commit(ctx); err != nil {
		return stored, err
	}
	return stored, nil
}

func (s *Postgres) IdempotentUpsert(ctx context.Context, key, operation, payloadHash, code string, inputs []domain.ObservationInput, defaultSource string) (int, []byte, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, nil, false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
		return 0, nil, false, err
	}
	var existingOperation, existingHash string
	var response []byte
	err = tx.QueryRow(ctx, `SELECT operation, payload_hash, response FROM index_idempotency_records WHERE key = $1`, key).Scan(&existingOperation, &existingHash, &response)
	if err == nil {
		if existingOperation != operation || existingHash != payloadHash {
			return 0, nil, true, errors.New("idempotency key was already used with a different request")
		}
		if err := tx.Commit(ctx); err != nil {
			return 0, nil, true, err
		}
		return 0, response, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, false, err
	}
	stored, err := s.Upsert(ctx, code, inputs, defaultSource)
	if err != nil {
		return 0, nil, false, err
	}
	var summary any = map[string]int{"observations_received": len(inputs), "observations_revised": stored}
	if operation == "selic.csv.import" {
		minimum, maximum := "", ""
		for _, item := range inputs {
			if minimum == "" || item.Date < minimum {
				minimum = item.Date
			}
			if maximum == "" || item.Date > maximum {
				maximum = item.Date
			}
		}
		summary = map[string]any{"rows_parsed": len(inputs), "rows_upserted": stored, "min_date": minimum, "max_date": maximum, "source": defaultSource}
	}
	response, err = json.Marshal(summary)
	if err != nil {
		return 0, nil, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO index_idempotency_records (key, operation, payload_hash, response) VALUES ($1, $2, $3, $4::jsonb)`, key, operation, payloadHash, string(response))
	if err != nil {
		return 0, nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, false, err
	}
	return stored, response, false, nil
}

func (s *Postgres) StartRun(ctx context.Context, source string, from, to time.Time) (string, error) {
	id, err := newUUID()
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO index_sync_runs (id, source, status, from_date, to_date) VALUES ($1, $2, 'running', $3, $4)`, id, source, from, to)
	return id, err
}

func (s *Postgres) FinishRun(ctx context.Context, id, status string, seen, stored int, runErr error) error {
	var message *string
	if runErr != nil {
		text := runErr.Error()
		message = &text
	}
	_, err := s.pool.Exec(ctx, `UPDATE index_sync_runs SET status = $2, finished_at = NOW(), points_seen = $3, points_stored = $4, error = $5 WHERE id = $1`, id, status, seen, stored, message)
	return err
}

func (s *Postgres) RecentRuns(ctx context.Context, source string, limit int) ([]domain.SyncRun, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text, source, status, started_at, finished_at, from_date::text, to_date::text, points_seen, points_stored, error
		FROM index_sync_runs WHERE ($1 = '' OR source = $1) ORDER BY started_at DESC LIMIT $2`, source, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.SyncRun, 0)
	for rows.Next() {
		var item domain.SyncRun
		var from, to, message *string
		if err := rows.Scan(&item.ID, &item.Source, &item.Status, &item.StartedAt, &item.FinishedAt, &from, &to, &item.PointsSeen, &item.PointsStored, &message); err != nil {
			return nil, err
		}
		item.FromDate, item.ToDate, item.Error = from, to, message
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Postgres) AcquireSourceLock(ctx context.Context, key int64) (func(), bool, error) {
	conn, err := s.acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&locked); err != nil {
		conn.Release()
		return nil, false, err
	}
	if !locked {
		conn.Release()
		return nil, false, nil
	}
	return func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, key)
		conn.Release()
	}, true, nil
}

func scanSeries(row interface{ Scan(...any) error }) (domain.Series, error) {
	var item domain.Series
	err := row.Scan(&item.Code, &item.Group, &item.Name, &item.Unit, &item.Frequency, &item.Source, &item.Dimension, &item.Description, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanObservations(rows pgx.Rows) ([]domain.Observation, error) {
	items := make([]domain.Observation, 0)
	for rows.Next() {
		var item domain.Observation
		var attributes []byte
		if err := rows.Scan(&item.SeriesCode, &item.Dimension, &item.Date, &item.Value, &item.Revision, &item.IsCurrent, &item.Source, &item.RecordedAt, &attributes); err != nil {
			return nil, err
		}
		item.Attributes = append(json.RawMessage(nil), attributes...)
		if string(attributes) == "{}" {
			item.Attributes = nil
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func nullableDate(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func sameDecimal(a, b string) bool {
	ra, oka := new(big.Rat).SetString(strings.TrimSpace(a))
	rb, okb := new(big.Rat).SetString(strings.TrimSpace(b))
	return oka && okb && ra.Cmp(rb) == 0
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(b[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}
