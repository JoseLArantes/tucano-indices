-- +goose Up
CREATE TABLE IF NOT EXISTS index_series (
    code TEXT PRIMARY KEY,
    grp SMALLINT NOT NULL CHECK (grp BETWEEN 1 AND 6),
    name TEXT NOT NULL,
    unit TEXT NOT NULL,
    frequency TEXT NOT NULL,
    source TEXT NOT NULL,
    dimension TEXT,
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS index_observations (
    id UUID PRIMARY KEY,
    series_code TEXT NOT NULL REFERENCES index_series(code) ON DELETE CASCADE,
    dimension TEXT NOT NULL DEFAULT '',
    ref_date DATE NOT NULL,
    value NUMERIC(28, 12) NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    is_current BOOLEAN NOT NULL DEFAULT TRUE,
    source TEXT NOT NULL,
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (series_code, dimension, ref_date, revision)
);

CREATE UNIQUE INDEX IF NOT EXISTS index_observations_current_uq
    ON index_observations (series_code, dimension, ref_date) WHERE is_current;
CREATE INDEX IF NOT EXISTS index_observations_series_date_idx
    ON index_observations (series_code, dimension, ref_date DESC) INCLUDE (value, source, recorded_at)
    WHERE is_current;
CREATE INDEX IF NOT EXISTS index_observations_series_recorded_idx
    ON index_observations (series_code, recorded_at DESC);

CREATE TABLE IF NOT EXISTS index_sync_runs (
    id UUID PRIMARY KEY,
    source TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    status TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'skipped')),
    from_date DATE,
    to_date DATE,
    points_seen INTEGER NOT NULL DEFAULT 0,
    points_stored INTEGER NOT NULL DEFAULT 0,
    error TEXT
);
CREATE INDEX IF NOT EXISTS index_sync_runs_source_started_idx
    ON index_sync_runs (source, started_at DESC);

CREATE TABLE IF NOT EXISTS index_idempotency_records (
    key TEXT PRIMARY KEY,
    operation TEXT NOT NULL,
    payload_hash TEXT NOT NULL,
    response JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO index_series (code, grp, name, unit, frequency, source, dimension, description) VALUES
    ('selic_meta', 1, 'Selic target rate', 'pct_aa', 'daily', 'bcb_sgs:432', NULL, 'Policy rate target set by Copom'),
    ('selic_over', 1, 'Selic overnight rate', 'pct_aa', 'daily', 'bcb_sgs:1178', NULL, 'Effective overnight Selic rate'),
    ('selic_apurada', 1, 'Selic rate observed', 'pct_aa', 'daily', 'bcb_novoselic', NULL, 'Daily observed Selic rate and transaction statistics'),
    ('cdi', 1, 'CDI annualized', 'pct_aa', 'daily', 'bcb_sgs:12', NULL, 'Interbank CDI benchmark for fixed income'),
    ('curva_di', 1, 'DI futures curve', 'pct_aa', 'daily', 'b3_partner:curva_di', 'tenor', 'DI1 settlement curve; dimension is contract tenor'),
    ('tjlp', 1, 'TJLP', 'pct_aa', 'quarterly', 'bcb_sgs:256', NULL, 'Long-term interest rate used in JCP linkage'),
    ('juro_real_ntnb', 1, 'Real yield NTN-B', 'pct_aa', 'daily', 'tesouro', 'maturity_year', 'Indicative Tesouro IPCA+ yields by maturity year'),
    ('ipca', 2, 'IPCA monthly', 'pct_am', 'monthly', 'bcb_sgs:433', NULL, 'Official consumer inflation month over month'),
    ('ipca_15', 2, 'IPCA-15 monthly', 'pct_am', 'monthly', 'bcb_sgs:7478', NULL, 'IPCA-15 monthly inflation preview'),
    ('ipca_12m', 2, 'IPCA trailing 12 months', 'pct', 'monthly', 'bcb_sgs:13522', NULL, 'Accumulated IPCA over 12 months'),
    ('igpm', 2, 'IGP-M monthly', 'pct_am', 'monthly', 'bcb_sgs:189', NULL, 'General price index used in contracts'),
    ('inpc', 2, 'INPC monthly', 'pct_am', 'monthly', 'bcb_sgs:188', NULL, 'National consumer price index'),
    ('focus_ipca', 2, 'Focus median IPCA', 'pct_aa', 'daily', 'bcb_focus', 'target_year', 'Market median IPCA expectation by calendar year'),
    ('focus_selic', 2, 'Focus median Selic', 'pct_aa', 'daily', 'bcb_focus', 'target_year', 'Market median Selic expectation by calendar year'),
    ('focus_usd', 2, 'Focus median USD/BRL', 'brl', 'daily', 'bcb_focus', 'target_year', 'Market median FX expectation by calendar year'),
    ('usdbrl_ptax', 3, 'USD/BRL PTAX sell', 'brl', 'daily', 'bcb_sgs:1', NULL, 'Official Bacen PTAX selling rate'),
    ('ibov', 4, 'Ibovespa', 'pts', 'daily', 'b3_statistics:IBOVESPA', NULL, 'Primary B3 equity benchmark'),
    ('ibrx100', 4, 'IBrX 100', 'pts', 'daily', 'b3_statistics:IBXX', NULL, 'B3 broad-market index of 100 liquid stocks'),
    ('ibrx50', 4, 'IBrX 50', 'pts', 'daily', 'b3_statistics:IBXL', NULL, 'B3 broad-market index of 50 liquid stocks'),
    ('ibra', 4, 'IBrA', 'pts', 'daily', 'b3_statistics:IBRA', NULL, 'B3 broad-market index'),
    ('smll', 4, 'Small Cap', 'pts', 'daily', 'b3_statistics:SMLL', NULL, 'B3 small-cap equity index'),
    ('mlcx', 4, 'MidLarge Cap', 'pts', 'daily', 'b3_statistics:MLCX', NULL, 'B3 mid- and large-cap equity index'),
    ('idiv', 4, 'IDIV', 'pts', 'daily', 'b3_statistics:IDIV', NULL, 'B3 dividend index'),
    ('ifix', 4, 'IFIX', 'pts', 'daily', 'b3_statistics:IFIX', NULL, 'B3 real-estate fund index'),
    ('ifnc', 4, 'IFNC', 'pts', 'daily', 'b3_statistics:IFNC', NULL, 'B3 financial sector index'),
    ('icon', 4, 'ICON', 'pts', 'daily', 'b3_statistics:ICON', NULL, 'B3 consumer sector index'),
    ('imat', 4, 'IMAT', 'pts', 'daily', 'b3_statistics:IMAT', NULL, 'B3 basic materials sector index'),
    ('imob', 4, 'IMOB', 'pts', 'daily', 'b3_statistics:IMOB', NULL, 'B3 real-estate sector index'),
    ('util', 4, 'UTIL', 'pts', 'daily', 'b3_statistics:UTIL', NULL, 'B3 utilities sector index'),
    ('ieex', 4, 'IEEX', 'pts', 'daily', 'b3_statistics:IEEX', NULL, 'B3 electric power sector index'),
    ('indx', 4, 'INDX', 'pts', 'daily', 'b3_statistics:INDX', NULL, 'B3 industrial sector index'),
    ('igcx', 4, 'IGCX', 'pts', 'daily', 'b3_statistics:IGCX', NULL, 'B3 differentiated corporate governance index'),
    ('igct', 4, 'IGCT', 'pts', 'daily', 'b3_statistics:IGCT', NULL, 'B3 trade corporate governance index'),
    ('ibc_br', 6, 'IBC-Br', 'pts', 'monthly', 'bcb_sgs:24363', NULL, 'Monthly activity proxy; not official GDP')
ON CONFLICT (code) DO UPDATE SET
    grp = EXCLUDED.grp,
    name = EXCLUDED.name,
    unit = EXCLUDED.unit,
    frequency = EXCLUDED.frequency,
    source = EXCLUDED.source,
    dimension = EXCLUDED.dimension,
    description = EXCLUDED.description,
    updated_at = NOW();

-- +goose Down
DROP TABLE IF EXISTS index_idempotency_records;
DROP TABLE IF EXISTS index_sync_runs;
DROP TABLE IF EXISTS index_observations;
DROP TABLE IF EXISTS index_series;
