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
	"fmt"
	"strings"

	goerrors "github.com/ajitpratap0/GoSQLX/pkg/errors"
	"github.com/ajitpratap0/GoSQLX/pkg/models"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
)

// parseMatchAgainst parses MySQL MATCH(...) AGAINST('text' [IN NATURAL LANGUAGE MODE | IN BOOLEAN MODE | WITH QUERY EXPANSION])
func (p *Parser) parseMatchAgainst(matchFunc *ast.FunctionCall) (ast.Expression, error) {
	p.advance() // Consume AGAINST
	if !p.isType(models.TokenTypeLParen) {
		return nil, p.expectedError("(")
	}
	p.advance() // Consume (

	// Parse search expression (just the primary - not full expression, to avoid IN being eaten)
	searchExpr, err := p.parsePrimaryExpression()
	if err != nil {
		return nil, goerrors.InvalidSyntaxError(
			fmt.Sprintf("failed to parse AGAINST expression: %v", err),
			p.currentLocation(),
			"",
		).WithCause(err)
	}

	// Consume optional mode keywords until we hit )
	mode := ""
	for !p.isType(models.TokenTypeRParen) && !p.isType(models.TokenTypeEOF) {
		mode += " " + p.currentToken.Token.Value
		p.advance()
	}

	if !p.isType(models.TokenTypeRParen) {
		return nil, p.expectedError(")")
	}
	p.advance() // Consume )

	// Represent as a binary expression: MATCH(cols) AGAINST(expr)
	// Store the search expr and mode as a function call named "AGAINST"
	againstFunc := &ast.FunctionCall{
		Name:      "AGAINST",
		Arguments: []ast.Expression{searchExpr},
	}
	if mode != "" {
		againstFunc.Arguments = append(againstFunc.Arguments, &ast.LiteralValue{
			Value: strings.TrimSpace(mode),
			Type:  "STRING",
		})
	}

	return &ast.BinaryExpression{
		Left:     matchFunc,
		Operator: "AGAINST",
		Right:    againstFunc,
	}, nil
}

// parseShowStatement parses MySQL SHOW commands:
//   - SHOW TABLES
//   - SHOW DATABASES
//   - SHOW CREATE TABLE name
//   - SHOW COLUMNS FROM name
//   - SHOW INDEX FROM name
func (p *Parser) parseShowStatement() (ast.Statement, error) {
	show := ast.GetShowStatement()

	upper := strings.ToUpper(p.currentToken.Token.Value)

	switch upper {
	case "TABLES":
		show.ShowType = "TABLES"
		p.advance()
		// Optional FROM database
		if p.isType(models.TokenTypeFrom) {
			p.advance()
			show.From = p.currentToken.Token.Value
			p.advance()
		}
	case "DATABASES":
		show.ShowType = "DATABASES"
		p.advance()
	case "CREATE":
		p.advance() // Consume CREATE
		if p.isType(models.TokenTypeTable) {
			show.ShowType = "CREATE TABLE"
			p.advance() // Consume TABLE
			name, err := p.parseQualifiedName()
			if err != nil {
				return nil, p.expectedError("table name")
			}
			show.ObjectName = name
		} else {
			show.ShowType = "CREATE " + strings.ToUpper(p.currentToken.Token.Value)
			p.advance()
			name, err := p.parseQualifiedName()
			if err != nil {
				return nil, p.expectedError("object name")
			}
			show.ObjectName = name
		}
	case "COLUMNS":
		show.ShowType = "COLUMNS"
		p.advance()
		if p.isType(models.TokenTypeFrom) {
			p.advance()
			name, err := p.parseQualifiedName()
			if err != nil {
				return nil, p.expectedError("table name")
			}
			show.ObjectName = name
		}
	case "INDEX", "INDEXES", "KEYS":
		show.ShowType = upper
		p.advance()
		if p.isType(models.TokenTypeFrom) {
			p.advance()
			name, err := p.parseQualifiedName()
			if err != nil {
				return nil, p.expectedError("table name")
			}
			show.ObjectName = name
		}
	case "STATUS", "VARIABLES":
		show.ShowType = upper
		p.advance()
	default:
		// Generic: SHOW <whatever>
		show.ShowType = upper
		p.advance()
	}

	return show, nil
}

// parseDescribeStatement parses DESCRIBE/DESC/EXPLAIN table_name
func (p *Parser) parseDescribeStatement() (ast.Statement, error) {
	// For EXPLAIN SELECT ..., defer to parseStatement for the SELECT
	// For DESCRIBE table_name, just parse the table name
	if p.isType(models.TokenTypeSelect) {
		// EXPLAIN SELECT ... - treat as describe with the query text
		// For now, just skip to parse the select
		p.advance()
		stmt, err := p.parseSelectWithSetOperations()
		if err != nil {
			return nil, err
		}
		// Wrap in a describe
		_ = stmt
		desc := ast.GetDescribeStatement()
		desc.TableName = "SELECT"
		return desc, nil
	}

	// Snowflake: DESCRIBE TABLE <name>, DESCRIBE VIEW <name>, DESCRIBE STAGE
	// <name>, etc. Also MySQL's DESCRIBE <db>.<table>. Accept and consume a
	// leading object-kind keyword (TABLE, VIEW, DATABASE, SCHEMA) before the
	// name so we don't fail on "DESCRIBE TABLE users".
	if p.isType(models.TokenTypeTable) || p.isType(models.TokenTypeView) ||
		p.isType(models.TokenTypeDatabase) ||
		strings.EqualFold(p.currentToken.Token.Value, "SCHEMA") ||
		strings.EqualFold(p.currentToken.Token.Value, "STAGE") ||
		strings.EqualFold(p.currentToken.Token.Value, "STREAM") ||
		strings.EqualFold(p.currentToken.Token.Value, "TASK") ||
		strings.EqualFold(p.currentToken.Token.Value, "PIPE") ||
		strings.EqualFold(p.currentToken.Token.Value, "FUNCTION") ||
		strings.EqualFold(p.currentToken.Token.Value, "PROCEDURE") ||
		strings.EqualFold(p.currentToken.Token.Value, "WAREHOUSE") {
		p.advance()
	}

	name, err := p.parseQualifiedName()
	if err != nil {
		return nil, p.expectedError("table name")
	}
	desc := ast.GetDescribeStatement()
	desc.TableName = name
	return desc, nil
}

// parseExplainStatement parses:
//
//	EXPLAIN [ANALYZE] [FORMAT[=]<ident>] <inner-stmt>
//	EXPLAIN <table_name>   // MySQL/MariaDB DESCRIBE synonym
//
// The caller has already consumed the EXPLAIN keyword. If the tokens that
// follow look like a statement start (SELECT / WITH / INSERT / UPDATE /
// DELETE / MERGE / nested EXPLAIN), they are parsed as the inner statement
// and wrapped in an ExplainStatement. Otherwise — and only when neither
// ANALYZE nor FORMAT was seen — the function delegates to
// parseDescribeStatement so MySQL/MariaDB's "EXPLAIN users" shorthand
// still yields a DescribeStatement. Other dialects reject the bare-name
// form with a clear error.
//
// Depth bookkeeping: p.depth / MaxRecursionDepth is shared across all
// recursive parse paths (expressions, CTEs, subqueries, nested EXPLAIN).
// A deeply nested EXPLAIN EXPLAIN ... SELECT chain consumes one level
// per EXPLAIN, so the cap (100) covers normal usage with room to spare
// even combined with deep CTEs and expressions.
//
// Known edge case: `EXPLAIN analyze` / `EXPLAIN format` always treat the
// identifier as the option keyword, never as a table name. The tokenizer
// loses the backtick/double-quote distinction, so the parser cannot tell
// `ANALYZE` (keyword) from `` `analyze` `` (quoted identifier). Users who
// need to DESCRIBE a table literally named "analyze" or "format" must
// spell it as `DESCRIBE "analyze"` / `DESCRIBE "format"` instead.
func (p *Parser) parseExplainStatement() (ast.Statement, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > MaxRecursionDepth {
		return nil, goerrors.InvalidSyntaxError(
			fmt.Sprintf("maximum recursion depth exceeded (%d) at EXPLAIN", MaxRecursionDepth),
			p.currentLocation(),
			"",
		)
	}

	analyze := false
	if p.isTokenMatch("ANALYZE") {
		p.advance()
		analyze = true
	}

	format := ""
	if p.isTokenMatch("FORMAT") {
		p.advance()
		// Accept both FORMAT=<ident> (MySQL) and FORMAT <ident> (permissive).
		if p.isType(models.TokenTypeEq) {
			p.advance()
		}
		if !p.isIdentifier() {
			return nil, p.expectedError("format identifier (TRADITIONAL, JSON, TREE) — string literals are not accepted")
		}
		// Normalize to upper-case so downstream consumers can compare
		// with a single canonical spelling.
		format = strings.ToUpper(p.currentToken.Token.Value)
		p.advance()
	}

	if isExplainInnerStart(p.currentToken.Token.Type) {
		inner, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		ex := ast.GetExplainStatement()
		ex.Statement = inner
		ex.Analyze = analyze
		ex.Format = format
		return ex, nil
	}

	// No statement-start tokens. If we've already seen ANALYZE or FORMAT
	// options, "EXPLAIN ANALYZE users" is not a valid DESCRIBE synonym —
	// force a clear error rather than silently dropping the options.
	// Non-MySQL dialects also reject the bare-name form so they see the
	// same message instead of an incongruous "expected table name" from
	// parseDescribeStatement.
	if analyze || format != "" || !p.isExplainDescribeDialect() {
		return nil, p.expectedError("SELECT, INSERT, UPDATE, DELETE, MERGE, WITH, or EXPLAIN after EXPLAIN")
	}

	// Bare EXPLAIN <table_name> — MySQL / MariaDB synonym for DESCRIBE.
	return p.parseDescribeStatement()
}

// isExplainInnerStart reports whether t can legitimately begin a statement
// that is a valid EXPLAIN inner. The list is tight on purpose: each entry
// must correspond to a real dispatch case in parseStatement, otherwise
// we'd whitelist a token and then blow up with an unhelpful
// "expected statement" error inside parseStatement. A bare identifier is
// deliberately not here — that path belongs to the MySQL/MariaDB
// EXPLAIN-as-DESCRIBE synonym handled by the caller.
func isExplainInnerStart(t models.TokenType) bool {
	switch t {
	case models.TokenTypeSelect,
		models.TokenTypeWith,
		models.TokenTypeInsert,
		models.TokenTypeUpdate,
		models.TokenTypeDelete,
		models.TokenTypeMerge,
		// Allow nested EXPLAIN — parser.depth / MaxRecursionDepth caps abuse.
		models.TokenTypeExplain:
		return true
	}
	return false
}

// isExplainDescribeDialect reports whether the current dialect accepts
// MySQL's "EXPLAIN <table>" shorthand for DESCRIBE. MySQL and MariaDB
// support it; other dialects don't.
//
// Reads through p.Dialect() rather than p.dialect directly so the default
// ("" → "postgresql") is resolved consistently: a parser built without
// WithDialect() observes the same EXPLAIN behaviour as one explicitly
// constructed with DialectPostgreSQL.
func (p *Parser) isExplainDescribeDialect() bool {
	switch p.Dialect() {
	case string(keywords.DialectMySQL),
		string(keywords.DialectMariaDB),
		string(keywords.DialectGeneric),
		string(keywords.DialectUnknown):
		return true
	}
	return false
}

// parseReplaceStatement parses MySQL REPLACE INTO statement
func (p *Parser) parseReplaceStatement() (ast.Statement, error) {
	// Expect INTO
	if !p.isType(models.TokenTypeInto) {
		return nil, p.expectedError("INTO")
	}
	p.advance()

	// Parse table name
	tableName, err := p.parseQualifiedName()
	if err != nil {
		return nil, p.expectedError("table name")
	}

	// Parse column list if present
	columns := make([]ast.Expression, 0)
	if p.isType(models.TokenTypeLParen) {
		p.advance()
		for {
			if !p.isIdentifier() {
				return nil, p.expectedError("column name")
			}
			columns = append(columns, &ast.Identifier{Name: p.currentToken.Token.Value})
			p.advance()
			if !p.isType(models.TokenTypeComma) {
				break
			}
			p.advance()
		}
		if !p.isType(models.TokenTypeRParen) {
			return nil, p.expectedError(")")
		}
		p.advance()
	}

	// Parse VALUES
	if !p.isType(models.TokenTypeValues) {
		return nil, p.expectedError("VALUES")
	}
	p.advance()

	values := make([][]ast.Expression, 0)
	for {
		if !p.isType(models.TokenTypeLParen) {
			if len(values) == 0 {
				return nil, p.expectedError("(")
			}
			break
		}
		p.advance()

		row := make([]ast.Expression, 0)
		for {
			expr, err := p.parseExpression()
			if err != nil {
				return nil, goerrors.InvalidSyntaxError(
					fmt.Sprintf("failed to parse value in REPLACE: %v", err),
					p.currentLocation(),
					"",
				).WithCause(err)
			}
			row = append(row, expr)
			if !p.isType(models.TokenTypeComma) {
				break
			}
			p.advance()
		}
		if !p.isType(models.TokenTypeRParen) {
			return nil, p.expectedError(")")
		}
		p.advance()
		values = append(values, row)

		if !p.isType(models.TokenTypeComma) {
			break
		}
		p.advance()
	}

	replStmt := ast.GetReplaceStatement()
	replStmt.TableName = tableName
	replStmt.Columns = append(replStmt.Columns, columns...)
	replStmt.Values = append(replStmt.Values, values...)
	return replStmt, nil
}
