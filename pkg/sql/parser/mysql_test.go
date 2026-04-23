// Copyright 2026 GoSQLX Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
)

// TestMySQLLimitOffsetSyntax tests MySQL-style LIMIT offset, count
func TestMySQLLimitOffsetSyntax(t *testing.T) {
	tests := []struct {
		name       string
		sql        string
		wantLimit  int
		wantOffset int
	}{
		{
			name:       "LIMIT offset, count",
			sql:        "SELECT * FROM posts LIMIT 10, 20",
			wantLimit:  20,
			wantOffset: 10,
		},
		{
			name:       "LIMIT count only",
			sql:        "SELECT * FROM posts LIMIT 5",
			wantLimit:  5,
			wantOffset: 0,
		},
		{
			name:       "LIMIT with ORDER BY",
			sql:        "SELECT * FROM posts ORDER BY id DESC LIMIT 0, 50",
			wantLimit:  50,
			wantOffset: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseWithDialect(tt.sql, keywords.DialectMySQL)
			if err != nil {
				t.Fatalf("ParseWithDialect failed: %v", err)
			}
			if len(result.Statements) == 0 {
				t.Fatal("expected at least one statement")
			}
			sel, ok := result.Statements[0].(*ast.SelectStatement)
			if !ok {
				t.Fatalf("expected SelectStatement, got %T", result.Statements[0])
			}
			if sel.Limit == nil {
				t.Fatal("expected non-nil Limit")
			}
			if *sel.Limit != tt.wantLimit {
				t.Errorf("Limit = %d, want %d", *sel.Limit, tt.wantLimit)
			}
			if tt.wantOffset > 0 {
				if sel.Offset == nil {
					t.Fatal("expected non-nil Offset")
				}
				if *sel.Offset != tt.wantOffset {
					t.Errorf("Offset = %d, want %d", *sel.Offset, tt.wantOffset)
				}
			}
		})
	}
}

// TestMySQLOnDuplicateKeyUpdate tests ON DUPLICATE KEY UPDATE parsing
func TestMySQLOnDuplicateKeyUpdate(t *testing.T) {
	sql := `INSERT INTO user_stats (user_id, login_count) VALUES (1, 1)
		ON DUPLICATE KEY UPDATE login_count = login_count + 1`

	result, err := ParseWithDialect(sql, keywords.DialectMySQL)
	if err != nil {
		t.Fatalf("ParseWithDialect failed: %v", err)
	}

	stmt, ok := result.Statements[0].(*ast.InsertStatement)
	if !ok {
		t.Fatalf("expected InsertStatement, got %T", result.Statements[0])
	}
	if stmt.OnDuplicateKey == nil {
		t.Fatal("expected non-nil OnDuplicateKey")
	}
	if len(stmt.OnDuplicateKey.Updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(stmt.OnDuplicateKey.Updates))
	}
	col, ok := stmt.OnDuplicateKey.Updates[0].Column.(*ast.Identifier)
	if !ok || col.Name != "login_count" {
		t.Errorf("expected column login_count, got %v", stmt.OnDuplicateKey.Updates[0].Column)
	}
}

// TestMySQLBacktickIdentifiers tests backtick-quoted identifiers
func TestMySQLBacktickIdentifiers(t *testing.T) {
	tests := []string{
		"SELECT `id`, `name` FROM `users`",
		"SELECT `tbl`.`col` FROM `mydb`.`tbl`",
		"SELECT `select` FROM `from`",
	}

	for _, sql := range tests {
		t.Run(sql, func(t *testing.T) {
			_, err := ParseWithDialect(sql, keywords.DialectMySQL)
			if err != nil {
				t.Fatalf("ParseWithDialect failed: %v", err)
			}
		})
	}
}

// TestMySQLShowStatements tests SHOW command parsing
func TestMySQLShowStatements(t *testing.T) {
	tests := []struct {
		sql      string
		showType string
		objName  string
	}{
		{"SHOW TABLES", "TABLES", ""},
		{"SHOW DATABASES", "DATABASES", ""},
		{"SHOW CREATE TABLE users", "CREATE TABLE", "users"},
	}

	for _, tt := range tests {
		t.Run(tt.sql, func(t *testing.T) {
			result, err := ParseWithDialect(tt.sql, keywords.DialectMySQL)
			if err != nil {
				t.Fatalf("ParseWithDialect failed: %v", err)
			}
			show, ok := result.Statements[0].(*ast.ShowStatement)
			if !ok {
				t.Fatalf("expected ShowStatement, got %T", result.Statements[0])
			}
			if show.ShowType != tt.showType {
				t.Errorf("ShowType = %q, want %q", show.ShowType, tt.showType)
			}
			if tt.objName != "" && show.ObjectName != tt.objName {
				t.Errorf("ObjectName = %q, want %q", show.ObjectName, tt.objName)
			}
		})
	}
}

// TestMySQLDescribeStatement tests DESCRIBE command parsing
func TestMySQLDescribeStatement(t *testing.T) {
	tests := []string{
		"DESCRIBE users",
		"DESCRIBE schema1.users",
	}

	for _, sql := range tests {
		t.Run(sql, func(t *testing.T) {
			result, err := ParseWithDialect(sql, keywords.DialectMySQL)
			if err != nil {
				t.Fatalf("ParseWithDialect failed: %v", err)
			}
			desc, ok := result.Statements[0].(*ast.DescribeStatement)
			if !ok {
				t.Fatalf("expected DescribeStatement, got %T", result.Statements[0])
			}
			if desc.TableName == "" {
				t.Error("expected non-empty TableName")
			}
		})
	}
}

// TestMySQLExplainStatement tests EXPLAIN parsing for SELECT, INSERT, UPDATE,
// DELETE, WITH, and ANALYZE / FORMAT option combinations.
func TestMySQLExplainStatement(t *testing.T) {
	cases := []struct {
		name    string
		sql     string
		analyze bool
		format  string
	}{
		{"bare EXPLAIN SELECT", "EXPLAIN SELECT 1", false, ""},
		{"EXPLAIN SELECT with WHERE", "EXPLAIN SELECT id FROM users WHERE id = 1", false, ""},
		{"EXPLAIN ANALYZE SELECT", "EXPLAIN ANALYZE SELECT 1", true, ""},
		{"EXPLAIN FORMAT=JSON SELECT", "EXPLAIN FORMAT=JSON SELECT 1", false, "JSON"},
		{"EXPLAIN FORMAT JSON SELECT", "EXPLAIN FORMAT JSON SELECT 1", false, "JSON"},
		{"EXPLAIN FORMAT lower-case normalised", "EXPLAIN FORMAT=json SELECT 1", false, "JSON"},
		{"EXPLAIN ANALYZE FORMAT=JSON SELECT", "EXPLAIN ANALYZE FORMAT=JSON SELECT 1", true, "JSON"},
		{"EXPLAIN WITH CTE", "EXPLAIN WITH cte AS (SELECT 1) SELECT * FROM cte", false, ""},
		{"EXPLAIN INSERT", "EXPLAIN INSERT INTO t (a) VALUES (1)", false, ""},
		{"EXPLAIN UPDATE", "EXPLAIN UPDATE t SET a = 1 WHERE id = 1", false, ""},
		{"EXPLAIN DELETE", "EXPLAIN DELETE FROM t WHERE id = 1", false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ParseWithDialect(tc.sql, keywords.DialectMySQL)
			if err != nil {
				t.Fatalf("ParseWithDialect(%q): %v", tc.sql, err)
			}
			defer ast.ReleaseAST(result)

			if len(result.Statements) != 1 {
				t.Fatalf("expected 1 statement, got %d", len(result.Statements))
			}
			ex, ok := result.Statements[0].(*ast.ExplainStatement)
			if !ok {
				t.Fatalf("expected *ast.ExplainStatement, got %T", result.Statements[0])
			}
			if ex.Analyze != tc.analyze {
				t.Errorf("Analyze: got %v, want %v", ex.Analyze, tc.analyze)
			}
			if ex.Format != tc.format {
				t.Errorf("Format: got %q, want %q", ex.Format, tc.format)
			}
			if ex.Statement == nil {
				t.Error("Statement: got nil, want non-nil inner")
			}
		})
	}
}

// TestMySQLExplainDescribeSynonym verifies the MySQL "EXPLAIN <tablename>"
// synonym still yields a DescribeStatement (not an ExplainStatement with a
// bare-identifier inner).
func TestMySQLExplainDescribeSynonym(t *testing.T) {
	cases := []string{
		"EXPLAIN users",
		"EXPLAIN schema1.orders",
	}
	for _, sql := range cases {
		t.Run(sql, func(t *testing.T) {
			result, err := ParseWithDialect(sql, keywords.DialectMySQL)
			if err != nil {
				t.Fatalf("ParseWithDialect(%q): %v", sql, err)
			}
			defer ast.ReleaseAST(result)

			desc, ok := result.Statements[0].(*ast.DescribeStatement)
			if !ok {
				t.Fatalf("expected *ast.DescribeStatement, got %T", result.Statements[0])
			}
			if desc.TableName == "" {
				t.Error("expected non-empty TableName")
			}
		})
	}
}

// TestMySQLExplainRejectsOptionsWithoutStatement verifies EXPLAIN ANALYZE
// and EXPLAIN FORMAT=... require a real statement inner; a bare table name
// after options is a parse error, not a silent drop.
func TestMySQLExplainRejectsOptionsWithoutStatement(t *testing.T) {
	cases := []string{
		"EXPLAIN ANALYZE users",
		"EXPLAIN FORMAT=JSON users",
	}
	for _, sql := range cases {
		t.Run(sql, func(t *testing.T) {
			_, err := ParseWithDialect(sql, keywords.DialectMySQL)
			if err == nil {
				t.Fatalf("ParseWithDialect(%q): expected error, got nil", sql)
			}
		})
	}
}

// TestMySQLExplainNestedDepthCap verifies that pathologically nested
// EXPLAIN EXPLAIN ... SELECT is rejected rather than blowing the stack.
func TestMySQLExplainNestedDepthCap(t *testing.T) {
	// MaxRecursionDepth = 100; build a chain well beyond that.
	sql := strings.Repeat("EXPLAIN ", MaxRecursionDepth+5) + "SELECT 1"
	_, err := ParseWithDialect(sql, keywords.DialectMySQL)
	if err == nil {
		t.Fatal("expected depth-cap error for deeply nested EXPLAIN, got nil")
	}
	if !strings.Contains(err.Error(), "recursion depth") {
		t.Errorf("expected recursion-depth error, got: %v", err)
	}
}

// TestExplainBareNameRejectedOutsideMySQLFamily verifies that the
// "EXPLAIN <table>" DESCRIBE synonym is accepted for MySQL/MariaDB/generic
// but rejected with a clean error on strict dialects like PostgreSQL.
func TestExplainBareNameRejectedOutsideMySQLFamily(t *testing.T) {
	t.Run("MySQL accepts", func(t *testing.T) {
		result, err := ParseWithDialect("EXPLAIN users", keywords.DialectMySQL)
		if err != nil {
			t.Fatalf("MySQL should accept EXPLAIN users: %v", err)
		}
		defer ast.ReleaseAST(result)
		if _, ok := result.Statements[0].(*ast.DescribeStatement); !ok {
			t.Errorf("expected DescribeStatement, got %T", result.Statements[0])
		}
	})
	t.Run("PostgreSQL rejects", func(t *testing.T) {
		_, err := ParseWithDialect("EXPLAIN users", keywords.DialectPostgreSQL)
		if err == nil {
			t.Fatal("PostgreSQL should reject EXPLAIN <table>, got nil error")
		}
		if !strings.Contains(err.Error(), "after EXPLAIN") {
			t.Errorf("expected error mentioning EXPLAIN context, got: %v", err)
		}
	})
	t.Run("Snowflake rejects", func(t *testing.T) {
		_, err := ParseWithDialect("EXPLAIN users", keywords.DialectSnowflake)
		if err == nil {
			t.Fatal("Snowflake should reject EXPLAIN <table>, got nil error")
		}
	})
	t.Run("ClickHouse rejects bare-name form", func(t *testing.T) {
		// ClickHouse supports `EXPLAIN PLAN/PIPELINE/SYNTAX SELECT`, but
		// that grammar is not yet wired. Tracked in the observer repo at
		// docs/issues/gosqlx-explain-clickhouse.md. Until then, the
		// bare-name form must at least reject cleanly.
		_, err := ParseWithDialect("EXPLAIN users", keywords.DialectClickHouse)
		if err == nil {
			t.Fatal("ClickHouse should reject EXPLAIN <table>, got nil error")
		}
	})
}

// TestExplainInnerMerge proves EXPLAIN MERGE (legitimately accepted by
// parseStatement) actually round-trips through the EXPLAIN path.
func TestExplainInnerMerge(t *testing.T) {
	sql := "EXPLAIN MERGE INTO t USING s ON t.id = s.id WHEN MATCHED THEN UPDATE SET t.a = s.a"
	result, err := ParseWithDialect(sql, keywords.DialectMySQL)
	if err != nil {
		t.Fatalf("ParseWithDialect: %v", err)
	}
	defer ast.ReleaseAST(result)
	ex, ok := result.Statements[0].(*ast.ExplainStatement)
	if !ok {
		t.Fatalf("expected ExplainStatement, got %T", result.Statements[0])
	}
	if ex.Statement == nil {
		t.Error("MERGE inner is nil")
	}
}

// TestExplainBareEOFError verifies "EXPLAIN" with no following tokens
// fails cleanly rather than yielding an empty ExplainStatement or panic.
func TestExplainBareEOFError(t *testing.T) {
	_, err := ParseWithDialect("EXPLAIN", keywords.DialectMySQL)
	if err == nil {
		t.Fatal("expected error for bare EXPLAIN, got nil")
	}
}

// TestExplainParseRoundTrip verifies the parsed ExplainStatement's SQL()
// method produces output that preserves the observable fields set by the
// parser (analyze flag, uppercase format).
func TestExplainParseRoundTrip(t *testing.T) {
	cases := []struct {
		sql  string
		want string
	}{
		{"EXPLAIN SELECT 1", "EXPLAIN "}, // SELECT inner's SQL() is dialect-dependent; just check prefix
		{"EXPLAIN ANALYZE SELECT 1", "EXPLAIN ANALYZE "},
		{"EXPLAIN FORMAT=json SELECT 1", "EXPLAIN FORMAT=JSON "}, // uppercased
		{"EXPLAIN ANALYZE FORMAT=tree SELECT 1", "EXPLAIN ANALYZE FORMAT=TREE "},
	}
	for _, tc := range cases {
		t.Run(tc.sql, func(t *testing.T) {
			result, err := ParseWithDialect(tc.sql, keywords.DialectMySQL)
			if err != nil {
				t.Fatalf("ParseWithDialect: %v", err)
			}
			defer ast.ReleaseAST(result)
			ex := result.Statements[0].(*ast.ExplainStatement)
			got := ex.SQL()
			if !strings.HasPrefix(got, tc.want) {
				t.Errorf("SQL(): %q does not start with %q", got, tc.want)
			}
			// Sanity: the inner's body must not be silently dropped.
			if !strings.Contains(got, "SELECT") {
				t.Errorf("SQL(): %q missing SELECT body from inner", got)
			}
		})
	}
}

// TestExplainAnalyzeIdentifierEdgeCase documents the known limitation:
// "EXPLAIN analyze" always treats the identifier as the ANALYZE option
// keyword, never as a table name. The tokenizer drops quoting information
// so the parser cannot distinguish `ANALYZE`, `` `analyze` ``, or
// `"analyze"` — all produce an IDENTIFIER token with value "analyze".
// Users who need to DESCRIBE a table literally named "analyze" must spell
// it as `DESCRIBE "analyze"` instead.
func TestExplainAnalyzeIdentifierEdgeCase(t *testing.T) {
	cases := []string{
		"EXPLAIN analyze",
		"EXPLAIN FORMAT",
	}
	for _, sql := range cases {
		t.Run(sql, func(t *testing.T) {
			_, err := ParseWithDialect(sql, keywords.DialectMySQL)
			if err == nil {
				t.Fatalf("expected error (ANALYZE/FORMAT are option keywords, not table names), got nil")
			}
		})
	}
}

// TestMySQLExplainShallowNesting verifies nesting within the cap works.
func TestMySQLExplainShallowNesting(t *testing.T) {
	result, err := ParseWithDialect("EXPLAIN EXPLAIN SELECT 1", keywords.DialectMySQL)
	if err != nil {
		t.Fatalf("ParseWithDialect: %v", err)
	}
	defer ast.ReleaseAST(result)

	outer, ok := result.Statements[0].(*ast.ExplainStatement)
	if !ok {
		t.Fatalf("outer: expected ExplainStatement, got %T", result.Statements[0])
	}
	inner, ok := outer.Statement.(*ast.ExplainStatement)
	if !ok {
		t.Fatalf("inner: expected ExplainStatement, got %T", outer.Statement)
	}
	if inner.Statement == nil {
		t.Error("innermost Statement is nil")
	}
}

// TestMySQLReplaceInto tests REPLACE INTO parsing
func TestMySQLReplaceInto(t *testing.T) {
	sql := "REPLACE INTO cache (key_name, value) VALUES ('k1', 'v1')"

	result, err := ParseWithDialect(sql, keywords.DialectMySQL)
	if err != nil {
		t.Fatalf("ParseWithDialect failed: %v", err)
	}

	stmt, ok := result.Statements[0].(*ast.ReplaceStatement)
	if !ok {
		t.Fatalf("expected ReplaceStatement, got %T", result.Statements[0])
	}
	if stmt.TableName != "cache" {
		t.Errorf("TableName = %q, want cache", stmt.TableName)
	}
	if len(stmt.Columns) != 2 {
		t.Errorf("expected 2 columns, got %d", len(stmt.Columns))
	}
	if len(stmt.Values) != 1 {
		t.Errorf("expected 1 value row, got %d", len(stmt.Values))
	}
}

// TestMySQLUpdateWithLimit tests UPDATE ... LIMIT
func TestMySQLUpdateWithLimit(t *testing.T) {
	sql := "UPDATE users SET active = 0 WHERE last_login < '2024-01-01' LIMIT 100"
	_, err := ParseWithDialect(sql, keywords.DialectMySQL)
	if err != nil {
		t.Fatalf("ParseWithDialect failed: %v", err)
	}
}

// TestMySQLDeleteWithLimit tests DELETE ... LIMIT
func TestMySQLDeleteWithLimit(t *testing.T) {
	sql := "DELETE FROM logs WHERE created_at < '2024-01-01' LIMIT 1000"
	_, err := ParseWithDialect(sql, keywords.DialectMySQL)
	if err != nil {
		t.Fatalf("ParseWithDialect failed: %v", err)
	}
}

// TestMySQLIntervalNumericSyntax tests INTERVAL 1 DAY style
func TestMySQLIntervalNumericSyntax(t *testing.T) {
	sql := "SELECT DATE_ADD(NOW(), INTERVAL 30 DAY) FROM dual"
	_, err := ParseWithDialect(sql, keywords.DialectMySQL)
	if err != nil {
		t.Fatalf("ParseWithDialect failed: %v", err)
	}
}

// TestMySQLIFFunction tests IF() function
func TestMySQLIFFunction(t *testing.T) {
	sql := "SELECT IF(salary > 50000, 'High', 'Low') FROM employees"
	_, err := ParseWithDialect(sql, keywords.DialectMySQL)
	if err != nil {
		t.Fatalf("ParseWithDialect failed: %v", err)
	}
}

// TestMySQLGroupConcat tests GROUP_CONCAT with SEPARATOR
func TestMySQLGroupConcat(t *testing.T) {
	sql := "SELECT GROUP_CONCAT(name ORDER BY name SEPARATOR ', ') FROM users GROUP BY dept"
	_, err := ParseWithDialect(sql, keywords.DialectMySQL)
	if err != nil {
		t.Fatalf("ParseWithDialect failed: %v", err)
	}
}

// TestMySQLMatchAgainst tests MATCH AGAINST full-text search
func TestMySQLMatchAgainst(t *testing.T) {
	sql := "SELECT * FROM articles WHERE MATCH(title, content) AGAINST('search term' IN NATURAL LANGUAGE MODE)"
	_, err := ParseWithDialect(sql, keywords.DialectMySQL)
	if err != nil {
		t.Fatalf("ParseWithDialect failed: %v", err)
	}
}

// TestMySQLRegexp tests REGEXP operator
func TestMySQLRegexp(t *testing.T) {
	sql := "SELECT * FROM users WHERE email REGEXP '^[a-z]+@[a-z]+$'"
	_, err := ParseWithDialect(sql, keywords.DialectMySQL)
	if err != nil {
		t.Fatalf("ParseWithDialect failed: %v", err)
	}
}

// TestMySQLTestdataIntegration runs all 30 MySQL test files
func TestMySQLTestdataIntegration(t *testing.T) {
	files, err := filepath.Glob("../../../testdata/mysql/*.sql")
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	if len(files) == 0 {
		t.Skip("no MySQL test files found")
	}

	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read file: %v", err)
			}
			lines := strings.Split(string(data), "\n")
			var sqlLines []string
			for _, l := range lines {
				trimmed := strings.TrimSpace(l)
				if trimmed == "" || strings.HasPrefix(trimmed, "--") {
					continue
				}
				sqlLines = append(sqlLines, l)
			}
			sql := strings.Join(sqlLines, "\n")
			_, err = ParseWithDialect(sql, keywords.DialectMySQL)
			if err != nil {
				t.Fatalf("ParseWithDialect failed: %v", err)
			}
		})
	}
}
