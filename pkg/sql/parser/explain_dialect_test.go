package parser

import (
	"strings"
	"testing"

	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
)

// parseOneExplain parses sql under dialect and returns the single
// ExplainStatement, failing the test otherwise. The caller must
// ast.ReleaseAST the returned AST.
func parseOneExplain(t *testing.T, sql string, dialect keywords.SQLDialect) (*ast.AST, *ast.ExplainStatement) {
	t.Helper()
	result, err := ParseWithDialect(sql, dialect)
	if err != nil {
		t.Fatalf("ParseWithDialect(%q, %s): %v", sql, dialect, err)
	}
	if len(result.Statements) != 1 {
		ast.ReleaseAST(result)
		t.Fatalf("expected 1 statement, got %d", len(result.Statements))
	}
	ex, ok := result.Statements[0].(*ast.ExplainStatement)
	if !ok {
		ast.ReleaseAST(result)
		t.Fatalf("expected *ast.ExplainStatement, got %T", result.Statements[0])
	}
	return result, ex
}

// TestPostgreSQLExplainParenOptions — the parenthesised options form,
// per https://www.postgresql.org/docs/current/sql-explain.html. ANALYZE
// and FORMAT map onto the AST; other options are accepted and discarded.
func TestPostgreSQLExplainParenOptions(t *testing.T) {
	cases := []struct {
		name    string
		sql     string
		analyze bool
		format  string
	}{
		{"parens ANALYZE", "EXPLAIN (ANALYZE) SELECT 1", true, ""},
		{"parens FORMAT JSON", "EXPLAIN (FORMAT JSON) SELECT 1", false, "JSON"},
		{"parens ANALYZE, FORMAT JSON", "EXPLAIN (ANALYZE, FORMAT JSON) SELECT 1", true, "JSON"},
		{"parens ANALYZE TRUE", "EXPLAIN (ANALYZE TRUE) SELECT 1", true, ""},
		{"parens ANALYZE FALSE", "EXPLAIN (ANALYZE FALSE) SELECT 1", false, ""},
		{"parens ANALYZE OFF", "EXPLAIN (ANALYZE OFF) SELECT 1", false, ""},
		{"parens ANALYZE ON", "EXPLAIN (ANALYZE ON) SELECT 1", true, ""},
		{"parens ANALYZE 0", "EXPLAIN (ANALYZE 0) SELECT 1", false, ""},
		{"parens ANALYZE 1", "EXPLAIN (ANALYZE 1) SELECT 1", true, ""},
		{"british ANALYSE", "EXPLAIN (ANALYSE) SELECT 1", true, ""},
		{"format lower-case normalised", "EXPLAIN (format yaml) SELECT 1", false, "YAML"},
		{"discarded options", "EXPLAIN (VERBOSE, COSTS OFF, BUFFERS, WAL, ANALYZE) SELECT 1", true, ""},
		{"discarded with values", "EXPLAIN (TIMING FALSE, SUMMARY ON, FORMAT XML) SELECT 1", false, "XML"},
		{"bare form still works", "EXPLAIN ANALYZE SELECT 1", true, ""},
		{"bare FORMAT still works", "EXPLAIN FORMAT=JSON SELECT 1", false, "JSON"},
		{"inner WITH", "EXPLAIN (ANALYZE) WITH cte AS (SELECT 1) SELECT * FROM cte", true, ""},
	}
	for _, dialect := range []keywords.SQLDialect{keywords.DialectPostgreSQL, keywords.DialectRedshift} {
		for _, tc := range cases {
			t.Run(string(dialect)+"/"+tc.name, func(t *testing.T) {
				result, ex := parseOneExplain(t, tc.sql, dialect)
				defer ast.ReleaseAST(result)
				if ex.Analyze != tc.analyze {
					t.Errorf("Analyze: got %v, want %v", ex.Analyze, tc.analyze)
				}
				if ex.Format != tc.format {
					t.Errorf("Format: got %q, want %q", ex.Format, tc.format)
				}
				if ex.Statement == nil {
					t.Error("Statement: got nil, want non-nil inner")
				}
				if ex.Mode != "" {
					t.Errorf("Mode: got %q, want empty under %s", ex.Mode, dialect)
				}
			})
		}
	}
}

// TestPostgreSQLExplainParenErrors — malformed options lists fail loudly.
func TestPostgreSQLExplainParenErrors(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{"empty parens", "EXPLAIN () SELECT 1"},
		{"trailing comma", "EXPLAIN (ANALYZE,) SELECT 1"},
		{"bad bool", "EXPLAIN (ANALYZE MAYBE) SELECT 1"},
		{"missing close", "EXPLAIN (ANALYZE SELECT 1"},
		{"format without value", "EXPLAIN (FORMAT) SELECT 1"},
		{"no inner statement", "EXPLAIN (ANALYZE)"},
		{"mixed parens then bare ANALYZE", "EXPLAIN (ANALYZE FALSE) ANALYZE SELECT 1"},
		{"mixed parens then bare FORMAT", "EXPLAIN (ANALYZE) FORMAT JSON SELECT 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if result, err := ParseWithDialect(tc.sql, keywords.DialectPostgreSQL); err == nil {
				ast.ReleaseAST(result)
				t.Fatalf("ParseWithDialect(%q) succeeded, want error", tc.sql)
			}
		})
	}
}

// TestExplainParenOptionsRejectedOutsidePostgresFamily — no
// cross-dialect leakage: the parens form stays an error elsewhere.
func TestExplainParenOptionsRejectedOutsidePostgresFamily(t *testing.T) {
	sqls := []string{
		"EXPLAIN (ANALYZE) SELECT 1",
		"EXPLAIN (FORMAT JSON) SELECT 1",
		"EXPLAIN (ANALYZE, FORMAT JSON) SELECT 1",
	}
	for _, dialect := range []keywords.SQLDialect{
		keywords.DialectMySQL, keywords.DialectMariaDB,
		keywords.DialectSQLite, keywords.DialectClickHouse,
		keywords.DialectGeneric,
	} {
		for _, sql := range sqls {
			t.Run(string(dialect)+"/"+sql, func(t *testing.T) {
				if result, err := ParseWithDialect(sql, dialect); err == nil {
					ast.ReleaseAST(result)
					t.Fatalf("parens options parsed under %s, want error", dialect)
				}
			})
		}
	}
}

// TestClickHouseExplainModes — the ClickHouse modifier grammar, per
// https://clickhouse.com/docs/en/sql-reference/statements/explain.
func TestClickHouseExplainModes(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		mode string
	}{
		{"PLAN", "EXPLAIN PLAN SELECT 1", "PLAN"},
		{"PIPELINE", "EXPLAIN PIPELINE SELECT 1", "PIPELINE"},
		{"SYNTAX", "EXPLAIN SYNTAX SELECT 1", "SYNTAX"},
		{"AST", "EXPLAIN AST SELECT 1", "AST"},
		{"ESTIMATE", "EXPLAIN ESTIMATE SELECT 1", "ESTIMATE"},
		{"QUERY TREE", "EXPLAIN QUERY TREE SELECT 1", "QUERY TREE"},
		{"lower-case normalised", "explain plan select 1", "PLAN"},
		{"no modifier", "EXPLAIN SELECT 1", ""},
		{"settings discarded", "EXPLAIN PLAN header=1 SELECT 1", "PLAN"},
		{"multiple settings", "EXPLAIN PLAN header=1, actions=1 SELECT 1", "PLAN"},
		{"PIPELINE graph setting", "EXPLAIN PIPELINE graph=1 SELECT 1", "PIPELINE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, ex := parseOneExplain(t, tc.sql, keywords.DialectClickHouse)
			defer ast.ReleaseAST(result)
			if ex.Mode != tc.mode {
				t.Errorf("Mode: got %q, want %q", ex.Mode, tc.mode)
			}
			if ex.Statement == nil {
				t.Error("Statement: got nil, want non-nil inner")
			}
		})
	}
}

// TestClickHouseExplainModeSQLRoundTrip — .SQL() preserves the modifier
// with canonical casing.
func TestClickHouseExplainModeSQLRoundTrip(t *testing.T) {
	result, ex := parseOneExplain(t, "explain query tree select 1", keywords.DialectClickHouse)
	defer ast.ReleaseAST(result)
	sql := ex.SQL()
	if !strings.HasPrefix(sql, "EXPLAIN QUERY TREE ") {
		t.Fatalf("SQL() = %q, want EXPLAIN QUERY TREE prefix", sql)
	}
}

// TestClickHouseExplainErrors — malformed modifier/settings fail loudly.
func TestClickHouseExplainErrors(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{"QUERY without TREE", "EXPLAIN QUERY SELECT 1"},
		{"trailing settings comma", "EXPLAIN PLAN header=1, SELECT 1"},
		{"setting without value", "EXPLAIN PLAN header= SELECT 1"},
		{"modifier without inner", "EXPLAIN PLAN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if result, err := ParseWithDialect(tc.sql, keywords.DialectClickHouse); err == nil {
				ast.ReleaseAST(result)
				t.Fatalf("ParseWithDialect(%q) succeeded, want error", tc.sql)
			}
		})
	}
}

// TestClickHouseModifiersOutsideClickHouse — no cross-dialect leakage,
// with the documented carve-out: dialects WITHOUT the EXPLAIN-<table>
// DESCRIBE synonym reject the modifier strings outright; the MySQL
// family reads the modifier word as a table name per its own spec'd
// synonym (so "EXPLAIN PLAN SELECT 1" becomes DESCRIBE plan + a second
// SELECT statement — never a ClickHouse-moded ExplainStatement). The
// success criterion in the issue doc was amended to this carve-out:
// demanding failure there would contradict the MySQL synonym contract.
func TestClickHouseModifiersOutsideClickHouse(t *testing.T) {
	for _, dialect := range []keywords.SQLDialect{
		keywords.DialectPostgreSQL, keywords.DialectSQLite,
		keywords.DialectSQLServer,
	} {
		t.Run(string(dialect), func(t *testing.T) {
			if result, err := ParseWithDialect("EXPLAIN PLAN SELECT 1", dialect); err == nil {
				ast.ReleaseAST(result)
				t.Fatalf("EXPLAIN PLAN parsed under %s, want error", dialect)
			}
		})
	}

	t.Run("mysql describe synonym", func(t *testing.T) {
		result, err := ParseWithDialect("EXPLAIN PLAN", keywords.DialectMySQL)
		if err != nil {
			t.Fatalf("MySQL EXPLAIN PLAN (describe synonym): %v", err)
		}
		defer ast.ReleaseAST(result)
		if _, ok := result.Statements[0].(*ast.DescribeStatement); !ok {
			t.Fatalf("MySQL EXPLAIN PLAN: got %T, want *ast.DescribeStatement (table named plan)", result.Statements[0])
		}
	})

	t.Run("mysql modifier+statement is describe, never a moded explain", func(t *testing.T) {
		result, err := ParseWithDialect("EXPLAIN PLAN SELECT 1", keywords.DialectMySQL)
		if err != nil {
			// Also acceptable: rejected outright. The pinned property is
			// only that it can never produce a Mode-carrying explain.
			return
		}
		defer ast.ReleaseAST(result)
		for _, stmt := range result.Statements {
			if ex, ok := stmt.(*ast.ExplainStatement); ok && ex.Mode != "" {
				t.Fatalf("MySQL produced a ClickHouse-moded ExplainStatement: %+v", ex)
			}
		}
		if _, ok := result.Statements[0].(*ast.DescribeStatement); !ok {
			t.Fatalf("first statement: got %T, want *ast.DescribeStatement", result.Statements[0])
		}
	})
}
