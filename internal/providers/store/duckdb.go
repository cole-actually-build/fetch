package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cole/fetch/internal/core"
	_ "github.com/marcboeker/go-duckdb"
)

var _ Store = (*DuckDB)(nil)

// DuckDB implements Store over a single DuckDB database file.
type DuckDB struct {
	db *sql.DB
}

// OpenDuckDB opens (creating if needed) the database and ensures meta tables.
func OpenDuckDB(path string) (*DuckDB, error) {
	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, err
	}
	d := &DuckDB{db: db}
	if err := d.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

func (d *DuckDB) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS runs (
			id VARCHAR PRIMARY KEY,
			pipeline_id VARCHAR,
			input JSON,
			status VARCHAR,
			started_at TIMESTAMP,
			finished_at TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS step_traces (
			run_id VARCHAR,
			step_id VARCHAR,
			status VARCHAR,
			input_summary VARCHAR,
			output_summary VARCHAR,
			artifact_refs JSON,
			tokens BIGINT,
			error VARCHAR,
			fallback_used BOOLEAN
		)`,
	}
	for _, s := range stmts {
		if _, err := d.db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (d *DuckDB) Close() error { return d.db.Close() }

func sanitize(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func duckType(t core.FieldType) string {
	switch t {
	case core.FieldInt:
		return "BIGINT"
	case core.FieldFloat:
		return "DOUBLE"
	case core.FieldBool:
		return "BOOLEAN"
	case core.FieldTimestamp:
		return "TIMESTAMP"
	default:
		return "VARCHAR"
	}
}

func (d *DuckDB) tableName(pipelineID string) string { return "data_" + sanitize(pipelineID) }

func (d *DuckDB) EnsureTable(ctx context.Context, pipelineID string, fields []core.Field) error {
	cols := make([]string, 0, len(fields)+2)
	for _, f := range fields {
		cols = append(cols, fmt.Sprintf("%s %s", sanitize(f.Name), duckType(f.Type)))
	}
	cols = append(cols, "__run_id VARCHAR", "__fetched_at TIMESTAMP")
	stmt := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", d.tableName(pipelineID), strings.Join(cols, ", "))
	_, err := d.db.ExecContext(ctx, stmt)
	return err
}

func (d *DuckDB) AppendRows(ctx context.Context, pipelineID string, fields []core.Field, runID string, rows []map[string]any) error {
	if len(rows) == 0 {
		return nil
	}
	colNames := make([]string, 0, len(fields)+2)
	for _, f := range fields {
		colNames = append(colNames, sanitize(f.Name))
	}
	colNames = append(colNames, "__run_id", "__fetched_at")

	placeholders := make([]string, len(colNames))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	// __fetched_at uses now() so the last placeholder is dropped for it.
	insertCols := strings.Join(colNames[:len(colNames)-1], ", ")
	insertPH := strings.Join(placeholders[:len(placeholders)-1], ", ")
	stmt := fmt.Sprintf("INSERT INTO %s (%s, __fetched_at) VALUES (%s, now())",
		d.tableName(pipelineID), insertCols, insertPH)

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	prepared, err := tx.PrepareContext(ctx, stmt)
	if err != nil {
		return err
	}
	for _, row := range rows {
		args := make([]any, 0, len(fields)+1)
		for _, f := range fields {
			args = append(args, row[f.Name])
		}
		args = append(args, runID)
		if _, err := prepared.ExecContext(ctx, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DuckDB) Query(ctx context.Context, query string) ([]map[string]any, error) {
	rows, err := d.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			m[c] = vals[i]
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (d *DuckDB) RecordRun(ctx context.Context, r core.Run) error {
	inputJSON, _ := json.Marshal(r.Input)
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO runs (id, pipeline_id, input, status, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET status = excluded.status, finished_at = excluded.finished_at`,
		r.ID, r.PipelineID, string(inputJSON), string(r.Status), r.StartedAt, r.FinishedAt,
	)
	return err
}

func (d *DuckDB) RecordTrace(ctx context.Context, t core.StepTrace) error {
	refsJSON, _ := json.Marshal(t.ArtifactRefs)
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO step_traces (run_id, step_id, status, input_summary, output_summary, artifact_refs, tokens, error, fallback_used)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.RunID, t.StepID, t.Status, t.InputSummary, t.OutputSummary, string(refsJSON), t.Tokens, t.Error, t.FallbackUsed,
	)
	return err
}
