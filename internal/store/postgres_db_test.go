package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/JoseLArantes/tucano-indices/internal/domain"
)

type fakeRow struct {
	values []any
	err    error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("Scan received %d destinations for %d values", len(dest), len(r.values))
	}
	for i := range dest {
		if err := assignScanValue(dest[i], r.values[i]); err != nil {
			return fmt.Errorf("scan column %d: %w", i, err)
		}
	}
	return nil
}

func assignScanValue(destination, value any) error {
	target := reflect.ValueOf(destination)
	if target.Kind() != reflect.Pointer || target.IsNil() {
		return fmt.Errorf("destination %T is not a pointer", destination)
	}
	target = target.Elem()
	if target.Kind() == reflect.Pointer {
		if value == nil {
			target.Set(reflect.Zero(target.Type()))
			return nil
		}
		if source, ok := value.(reflect.Value); ok {
			value = source.Interface()
		}
		if source := reflect.ValueOf(value); source.IsValid() && source.Kind() == reflect.Pointer {
			if source.IsNil() {
				target.Set(reflect.Zero(target.Type()))
				return nil
			}
			value = source.Elem().Interface()
		}
		allocated := reflect.New(target.Type().Elem())
		if err := assignScanValue(allocated.Interface(), value); err != nil {
			return err
		}
		target.Set(allocated)
		return nil
	}
	source := reflect.ValueOf(value)
	if !source.IsValid() {
		target.Set(reflect.Zero(target.Type()))
		return nil
	}
	if source.Type().AssignableTo(target.Type()) {
		target.Set(source)
		return nil
	}
	if source.Type().ConvertibleTo(target.Type()) {
		target.Set(source.Convert(target.Type()))
		return nil
	}
	return fmt.Errorf("cannot assign %T to %s", value, target.Type())
}

type fakeRows struct {
	values  [][]any
	index   int
	err     error
	scanErr error
	closed  bool
}

func (r *fakeRows) Close()                                       { r.closed = true }
func (r *fakeRows) Err() error                                   { return r.err }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.NewCommandTag("SELECT 0") }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Next() bool {
	if r.closed || r.index >= len(r.values) {
		return false
	}
	r.index++
	return true
}
func (r *fakeRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	if r.index == 0 || r.index > len(r.values) {
		return errors.New("Scan called without a current row")
	}
	return (fakeRow{values: r.values[r.index-1]}).Scan(dest...)
}
func (r *fakeRows) Values() ([]any, error) {
	if r.index == 0 || r.index > len(r.values) {
		return nil, errors.New("Values called without a current row")
	}
	return r.values[r.index-1], nil
}
func (r *fakeRows) RawValues() [][]byte  { return nil }
func (r *fakeRows) Conn() *pgx.Conn      { return nil }
func (r *fakeRows) TypeMap() *pgtype.Map { return pgtype.NewMap() }

type fakeTx struct {
	queryRows   []fakeRow
	execCalls   []queryCall
	execErrAt   int
	execErr     error
	commitErr   error
	rollbackErr error
	commits     int
	rollbacks   int
}

func (tx *fakeTx) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	if len(tx.queryRows) == 0 {
		return fakeRow{err: fmt.Errorf("unexpected transaction QueryRow: %s", query)}
	}
	row := tx.queryRows[0]
	tx.queryRows = tx.queryRows[1:]
	return row
}
func (tx *fakeTx) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	tx.execCalls = append(tx.execCalls, queryCall{query: query, args: args})
	if tx.execErr != nil && (tx.execErrAt == 0 || tx.execErrAt == len(tx.execCalls)) {
		return pgconn.CommandTag{}, tx.execErr
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}
func (tx *fakeTx) Commit(context.Context) error {
	tx.commits++
	return tx.commitErr
}
func (tx *fakeTx) Rollback(context.Context) error {
	tx.rollbacks++
	return tx.rollbackErr
}

type queryCall struct {
	query string
	args  []any
}

type fakePool struct {
	rows       *fakeRows
	queryErr   error
	queryCalls []queryCall
	rowResults []fakeRow
	rowCalls   []queryCall
	execCalls  []queryCall
	execErr    error
	txPlans    []*fakeTx
	beginErr   error
	begins     int
	pingErr    error
}

func (p *fakePool) Ping(context.Context) error { return p.pingErr }
func (p *fakePool) Query(_ context.Context, query string, args ...any) (pgx.Rows, error) {
	p.queryCalls = append(p.queryCalls, queryCall{query: query, args: args})
	if p.queryErr != nil {
		return nil, p.queryErr
	}
	if p.rows == nil {
		return &fakeRows{}, nil
	}
	return p.rows, nil
}
func (p *fakePool) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	p.rowCalls = append(p.rowCalls, queryCall{query: query, args: args})
	if len(p.rowResults) == 0 {
		return fakeRow{err: fmt.Errorf("unexpected QueryRow: %s", query)}
	}
	row := p.rowResults[0]
	p.rowResults = p.rowResults[1:]
	return row
}
func (p *fakePool) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	p.execCalls = append(p.execCalls, queryCall{query: query, args: args})
	if p.execErr != nil {
		return pgconn.CommandTag{}, p.execErr
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func (p *fakePool) BeginTx(context.Context, pgx.TxOptions) (transaction, error) {
	p.begins++
	if p.beginErr != nil {
		return nil, p.beginErr
	}
	if len(p.txPlans) == 0 {
		return &fakeTx{}, nil
	}
	tx := p.txPlans[0]
	p.txPlans = p.txPlans[1:]
	return tx, nil
}

func newFakePostgres(pool *fakePool) *Postgres {
	return &Postgres{pool: pool, acquire: func(context.Context) (sourceConnection, error) {
		return nil, errors.New("unexpected connection acquire")
	}}
}

func TestListAndGetSeries(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	dimension := "target_year"
	pool := &fakePool{rows: &fakeRows{values: [][]any{{"focus_ipca", int16(2), "Focus IPCA", "pct", "daily", "bcb_focus", &dimension, "expectations", created, created}}}}
	db := newFakePostgres(pool)
	items, err := db.ListSeries(context.Background(), 2, []string{"focus_ipca"})
	if err != nil || len(items) != 1 || items[0].Code != "focus_ipca" || items[0].Dimension == nil || *items[0].Dimension != dimension {
		t.Fatalf("ListSeries() = %#v, %v", items, err)
	}
	if !strings.Contains(pool.queryCalls[0].query, "cardinality($2::text[]) = 0") || pool.queryCalls[0].args[0] != 2 {
		t.Fatalf("unexpected catalog query: %#v", pool.queryCalls[0])
	}

	pool = &fakePool{rowResults: []fakeRow{{values: []any{"focus_ipca", int16(2), "Focus IPCA", "pct", "daily", "bcb_focus", &dimension, "expectations", created, created}}}}
	db = newFakePostgres(pool)
	series, err := db.GetSeries(context.Background(), " FOCUS_IPCA ")
	if err != nil || series.Code != "focus_ipca" || pool.rowCalls[0].args[0] != "focus_ipca" {
		t.Fatalf("GetSeries() = %#v, %v; args=%v", series, err, pool.rowCalls[0].args)
	}
}

func TestObservationQueriesAndChartResolutions(t *testing.T) {
	recorded := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	row := []any{"ibov", "", "2026-01-02", "130.50", int32(2), true, "b3", recorded, []byte(`{"exchange":"b3"}`)}
	for _, test := range []struct {
		name string
		run  func(*Postgres) error
	}{
		{name: "latest", run: func(db *Postgres) error { _, err := db.Latest(context.Background(), " IBOV ", ""); return err }},
		{name: "latest as of", run: func(db *Postgres) error {
			_, err := db.LatestAsOf(context.Background(), "IBOV", "", "2026-01-02")
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool := &fakePool{rows: &fakeRows{values: [][]any{row}}}
			if err := test.run(newFakePostgres(pool)); err != nil {
				t.Fatal(err)
			}
			if len(pool.queryCalls) != 1 || !strings.Contains(pool.queryCalls[0].query, "is_current") {
				t.Fatalf("query not captured: %#v", pool.queryCalls)
			}
		})
	}

	for _, resolution := range []string{"raw", "weekly", "monthly"} {
		t.Run(resolution, func(t *testing.T) {
			pool := &fakePool{rows: &fakeRows{values: [][]any{row}}}
			db := newFakePostgres(pool)
			points, err := db.Series(context.Background(), "IBOV", SeriesQuery{From: "2026-01-01", To: "2026-01-31", AsOf: "2026-01-31", Limit: 25, Offset: 2, Resolution: resolution})
			if err != nil || len(points) != 1 || points[0].X != "2026-01-02" || points[0].Y != "130.50" || string(points[0].Attributes) != `{"exchange":"b3"}` {
				t.Fatalf("Series(%s) = %#v, %v", resolution, points, err)
			}
			if pool.queryCalls[0].args[0] != "ibov" || pool.queryCalls[0].args[len(pool.queryCalls[0].args)-2] != 25 || pool.queryCalls[0].args[len(pool.queryCalls[0].args)-1] != 2 {
				t.Fatalf("unexpected chart query args: %#v", pool.queryCalls[0].args)
			}
		})
	}
	if _, err := newFakePostgres(&fakePool{}).Series(context.Background(), "ibov", SeriesQuery{Resolution: "hourly"}); err == nil {
		t.Fatal("Series accepted unsupported resolution")
	}

	pool := &fakePool{rows: &fakeRows{values: [][]any{row}}}
	observations, err := newFakePostgres(pool).ObservationSeries(context.Background(), "ibov", SeriesQuery{Resolution: "monthly"})
	if err != nil || len(observations) != 1 || !observations[0].IsCurrent || observations[0].Revision != 2 {
		t.Fatalf("ObservationSeries() = %#v, %v", observations, err)
	}
	if !strings.Contains(pool.queryCalls[0].query, "date_trunc($6, ref_date)") {
		t.Fatalf("monthly query is not bucketed: %s", pool.queryCalls[0].query)
	}
	if _, err := newFakePostgres(&fakePool{}).ObservationSeries(context.Background(), "ibov", SeriesQuery{Resolution: "yearly"}); err == nil {
		t.Fatal("ObservationSeries accepted unsupported resolution")
	}

}

func TestDateAndCountQueries(t *testing.T) {
	wantDate := time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC)
	pool := &fakePool{rowResults: []fakeRow{{values: []any{&wantDate}}, {values: []any{int64(22)}}}}
	db := newFakePostgres(pool)
	got, err := db.MaxDate(context.Background(), " IBOV ", "")
	if err != nil || !got.Equal(wantDate) || pool.rowCalls[0].args[0] != "ibov" {
		t.Fatalf("MaxDate() = %v, %v", got, err)
	}
	count, err := db.CountCurrent(context.Background(), "IBOV", "2026-01-01", "2026-03-01")
	if err != nil || count != 22 || pool.rowCalls[1].args[0] != "ibov" {
		t.Fatalf("CountCurrent() = %d, %v", count, err)
	}
	pool = &fakePool{rowResults: []fakeRow{{values: []any{nil}}}}
	got, err = newFakePostgres(pool).MaxDate(context.Background(), "ibov", "")
	if err != nil || !got.IsZero() {
		t.Fatalf("empty MaxDate() = %v, %v", got, err)
	}
}

func TestUpsertCreatesNoOpsAndRevisions(t *testing.T) {
	ctx := context.Background()
	input := domain.ObservationInput{Date: "2026-01-01", Value: "1.2500", Attributes: []byte(`{"unit":"pct"}`)}

	newTx := &fakeTx{queryRows: []fakeRow{{err: pgx.ErrNoRows}}}
	pool := &fakePool{txPlans: []*fakeTx{newTx}}
	stored, err := newFakePostgres(pool).Upsert(ctx, " IBOV ", []domain.ObservationInput{input}, "manual")
	if err != nil || stored != 1 || pool.begins != 1 {
		t.Fatalf("new Upsert() = %d, %v", stored, err)
	}
	if newTx.commits != 1 || len(newTx.execCalls) != 1 || !strings.Contains(newTx.execCalls[0].query, "INSERT INTO index_observations") {
		t.Fatalf("new observation transaction = %#v", newTx)
	}

	sameTx := &fakeTx{queryRows: []fakeRow{{values: []any{"1.25", int32(3)}}}}
	pool = &fakePool{txPlans: []*fakeTx{sameTx}}
	stored, err = newFakePostgres(pool).Upsert(ctx, "ibov", []domain.ObservationInput{input}, "manual")
	if err != nil || stored != 0 || sameTx.commits != 1 || len(sameTx.execCalls) != 1 || !strings.Contains(sameTx.execCalls[0].query, "attributes = $6::jsonb") {
		t.Fatalf("same-value Upsert() = %d, %v tx=%#v", stored, err, sameTx)
	}
	if sameTx.execCalls[0].args[4] != "manual" {
		t.Fatalf("default source not applied: %#v", sameTx.execCalls[0].args)
	}

	revisionTx := &fakeTx{queryRows: []fakeRow{{values: []any{"1", int32(3)}}}}
	pool = &fakePool{txPlans: []*fakeTx{revisionTx}}
	stored, err = newFakePostgres(pool).Upsert(ctx, "ibov", []domain.ObservationInput{{Date: input.Date, Value: "2", Source: "correction"}}, "manual")
	if err != nil || stored != 1 || revisionTx.commits != 1 || len(revisionTx.execCalls) != 2 {
		t.Fatalf("revised Upsert() = %d, %v tx=%#v", stored, err, revisionTx)
	}
	if !strings.Contains(revisionTx.execCalls[0].query, "SET is_current = FALSE") || revisionTx.execCalls[1].args[5] != int32(4) || revisionTx.execCalls[1].args[6] != "correction" {
		t.Fatalf("revision transaction was incorrect: %#v", revisionTx.execCalls)
	}
}

func TestUpsertValidationAndRollback(t *testing.T) {
	for _, input := range []domain.ObservationInput{{Date: "bad", Value: "1"}, {Date: "2026-01-01", Value: "not-number"}} {
		tx := &fakeTx{}
		pool := &fakePool{txPlans: []*fakeTx{tx}}
		if _, err := newFakePostgres(pool).Upsert(context.Background(), "ibov", []domain.ObservationInput{input}, "api"); err == nil {
			t.Errorf("Upsert(%#v) succeeded", input)
		}
		if tx.rollbacks != 1 || tx.commits != 0 {
			t.Errorf("invalid Upsert did not roll back: %#v", tx)
		}
	}
	if stored, err := newFakePostgres(&fakePool{}).Upsert(context.Background(), "ibov", nil, "api"); err != nil || stored != 0 {
		t.Fatalf("empty Upsert() = %d, %v", stored, err)
	}
	insertTx := &fakeTx{queryRows: []fakeRow{{err: pgx.ErrNoRows}}, execErrAt: 1, execErr: errors.New("insert failed")}
	pool := &fakePool{txPlans: []*fakeTx{insertTx}}
	if _, err := newFakePostgres(pool).Upsert(context.Background(), "ibov", []domain.ObservationInput{{Date: "2026-01-01", Value: "1"}}, "api"); err == nil || insertTx.rollbacks != 1 {
		t.Fatalf("insert error = %v, tx=%#v", err, insertTx)
	}
	pool = &fakePool{beginErr: errors.New("begin failed")}
	if _, err := newFakePostgres(pool).Upsert(context.Background(), "ibov", []domain.ObservationInput{{Date: "2026-01-01", Value: "1"}}, "api"); err == nil {
		t.Fatal("Upsert ignored BeginTx error")
	}
}

func TestIdempotentUpsertReplayConflictAndFreshWrite(t *testing.T) {
	ctx := context.Background()
	payload := []byte(`{"ok":true}`)
	replayTx := &fakeTx{queryRows: []fakeRow{{values: []any{"observation.upsert.ibov", "hash", payload}}}}
	pool := &fakePool{txPlans: []*fakeTx{replayTx}}
	stored, response, duplicate, err := newFakePostgres(pool).IdempotentUpsert(ctx, "key", "observation.upsert.ibov", "hash", "ibov", nil, "api")
	if err != nil || stored != 0 || !duplicate || string(response) != string(payload) || replayTx.commits != 1 {
		t.Fatalf("replay = %d %s %v %v", stored, response, duplicate, err)
	}

	conflictTx := &fakeTx{queryRows: []fakeRow{{values: []any{"other.operation", "other-hash", payload}}}}
	pool = &fakePool{txPlans: []*fakeTx{conflictTx}}
	_, _, duplicate, err = newFakePostgres(pool).IdempotentUpsert(ctx, "key", "observation.upsert.ibov", "hash", "ibov", nil, "api")
	if err == nil || !duplicate || conflictTx.rollbacks != 1 {
		t.Fatalf("conflict = duplicate %v, err %v, tx %#v", duplicate, err, conflictTx)
	}

	outer := &fakeTx{queryRows: []fakeRow{{err: pgx.ErrNoRows}}}
	inner := &fakeTx{queryRows: []fakeRow{{err: pgx.ErrNoRows}}}
	pool = &fakePool{txPlans: []*fakeTx{outer, inner}}
	input := domain.ObservationInput{Date: "2026-01-01", Value: "1"}
	stored, response, duplicate, err = newFakePostgres(pool).IdempotentUpsert(ctx, "new-key", "selic.csv.import", "csv-hash", "selic_apurada", []domain.ObservationInput{input}, "csv_import")
	if err != nil || stored != 1 || duplicate || outer.commits != 1 || inner.commits != 1 {
		t.Fatalf("fresh idempotent write = %d %s %v %v; outer=%#v inner=%#v", stored, response, duplicate, err, outer, inner)
	}
	if !strings.Contains(string(response), `"rows_parsed":1`) || !strings.Contains(string(response), `"min_date":"2026-01-01"`) {
		t.Fatalf("unexpected CSV import response: %s", response)
	}
}

func TestRunLedgerAndSourceLocks(t *testing.T) {
	ctx := context.Background()
	pool := &fakePool{}
	db := newFakePostgres(pool)
	id, err := db.StartRun(ctx, "b3", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	if err != nil || id == "" || len(pool.execCalls) != 1 || !strings.Contains(pool.execCalls[0].query, "INSERT INTO index_sync_runs") {
		t.Fatalf("StartRun() = %q, %v calls=%#v", id, err, pool.execCalls)
	}
	pool = &fakePool{}
	db = newFakePostgres(pool)
	message := errors.New("source timeout")
	if err := db.FinishRun(ctx, "run-id", "failed", 4, 2, message); err != nil || pool.execCalls[0].args[4] == nil || *pool.execCalls[0].args[4].(*string) != message.Error() {
		t.Fatalf("FinishRun() = %v args=%#v", err, pool.execCalls[0].args)
	}

	started := time.Now().UTC()
	finished := started.Add(time.Minute)
	rows := &fakeRows{values: [][]any{{"run-id", "b3", "succeeded", started, &finished, (*string)(nil), (*string)(nil), 10, 9, (*string)(nil)}}}
	pool = &fakePool{rows: rows}
	runs, err := newFakePostgres(pool).RecentRuns(ctx, "b3", 10)
	if err != nil || len(runs) != 1 || runs[0].ID != "run-id" || runs[0].FinishedAt == nil || runs[0].PointsSeen != 10 {
		t.Fatalf("RecentRuns() = %#v, %v", runs, err)
	}
	if _, err := newFakePostgres(&fakePool{}).RecentRuns(ctx, "", 201); err != nil {
		t.Fatalf("RecentRuns default limit failed: %v", err)
	}

	locked := &fakeSourceConn{row: fakeRow{values: []any{true}}}
	db = &Postgres{pool: &fakePool{}, acquire: func(context.Context) (sourceConnection, error) { return locked, nil }}
	unlock, acquired, err := db.AcquireSourceLock(ctx, 123)
	if err != nil || !acquired || unlock == nil || locked.released {
		t.Fatalf("AcquireSourceLock(lock=true): acquired=%v err=%v unlockPresent=%v", acquired, err, unlock != nil)
	}
	unlock()
	if locked.released != true || len(locked.execCalls) != 1 || !strings.Contains(locked.execCalls[0].query, "pg_advisory_unlock") {
		t.Fatalf("unlock was not released: %#v", locked)
	}
	unlocked := &fakeSourceConn{row: fakeRow{values: []any{false}}}
	db.acquire = func(context.Context) (sourceConnection, error) { return unlocked, nil }
	unlock, acquired, err = db.AcquireSourceLock(ctx, 123)
	if err != nil || acquired || unlock != nil || !unlocked.released {
		t.Fatalf("AcquireSourceLock(lock=false): acquired=%v err=%v unlockPresent=%v", acquired, err, unlock != nil)
	}
	db.acquire = func(context.Context) (sourceConnection, error) { return nil, errors.New("acquire failed") }
	if _, _, err := db.AcquireSourceLock(ctx, 123); err == nil {
		t.Fatal("AcquireSourceLock ignored acquire error")
	}
}

type fakeSourceConn struct {
	row       pgx.Row
	execCalls []queryCall
	released  bool
}

func (c *fakeSourceConn) QueryRow(context.Context, string, ...any) pgx.Row { return c.row }
func (c *fakeSourceConn) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	c.execCalls = append(c.execCalls, queryCall{query: query, args: args})
	return pgconn.NewCommandTag("SELECT 1"), nil
}
func (c *fakeSourceConn) Release() { c.released = true }

func TestScanObservationNullAttributesAndErrors(t *testing.T) {
	recorded := time.Now().UTC()
	rows := &fakeRows{values: [][]any{{"ibov", "", "2026-01-02", "10", int32(1), true, "b3", recorded, []byte(`{}`)}}}
	items, err := scanObservations(rows)
	if err != nil || len(items) != 1 || items[0].Attributes != nil {
		t.Fatalf("scanObservations() = %#v, %v", items, err)
	}
	rows = &fakeRows{values: [][]any{{"ibov"}}, scanErr: errors.New("bad row")}
	if _, err := scanObservations(rows); err == nil {
		t.Fatal("scanObservations ignored row scan error")
	}
	rows = &fakeRows{err: errors.New("iteration failed")}
	if _, err := scanObservations(rows); err == nil {
		t.Fatal("scanObservations ignored iteration error")
	}
}
