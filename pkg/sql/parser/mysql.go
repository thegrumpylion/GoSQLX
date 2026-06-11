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

	// ClickHouse EXPLAIN modifier: AST | SYNTAX | PLAN | PIPELINE |
	// ESTIMATE | QUERY TREE, optionally followed by a bare settings list
	// (header=1, actions=1, ...) which is consumed and discarded.
	// Dialect-gated: under every other dialect these stay plain
	// identifiers and fall through to the existing paths (for the
	// MySQL family that is the EXPLAIN-<table> DESCRIBE synonym).
	mode := ""
	if p.Dialect() == string(keywords.DialectClickHouse) {
		m, err := p.parseClickHouseExplainMode()
		if err != nil {
			return nil, err
		}
		mode = m
	}

	// PostgreSQL parenthesised options list: EXPLAIN (opt [val], ...).
	// Dialect-gated to PostgreSQL and Redshift (which inherits the
	// grammar); under other dialects LPAREN after EXPLAIN keeps failing
	// with the statement-start error below.
	analyze := false
	format := ""
	parenForm := false
	if p.isType(models.TokenTypeLParen) && p.isExplainParenOptionsDialect() {
		a, f, err := p.parseExplainParenOptions()
		if err != nil {
			return nil, err
		}
		analyze, format = a, f
		parenForm = true
		// PostgreSQL does not allow mixing the parenthesised form with
		// bare options; accepting the mix here would let a bare ANALYZE
		// silently override a parenthesised ANALYZE FALSE.
		if p.isTokenMatch("ANALYZE") || p.isTokenMatch("ANALYSE") || p.isTokenMatch("FORMAT") {
			return nil, p.expectedError("a statement after the EXPLAIN options list (bare options cannot be mixed with the parenthesised form)")
		}
	}

	if !parenForm && p.isTokenMatch("ANALYZE") {
		p.advance()
		analyze = true
	}

	if !parenForm && p.isTokenMatch("FORMAT") {
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
		ex.Mode = mode
		return ex, nil
	}

	// No statement-start tokens. If we've already seen ANALYZE or FORMAT
	// options, "EXPLAIN ANALYZE users" is not a valid DESCRIBE synonym —
	// force a clear error rather than silently dropping the options.
	// Non-MySQL dialects also reject the bare-name form so they see the
	// same message instead of an incongruous "expected table name" from
	// parseDescribeStatement.
	if analyze || format != "" || mode != "" || !p.isExplainDescribeDialect() {
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

// isExplainParenOptionsDialect reports whether the current dialect
// accepts PostgreSQL's parenthesised EXPLAIN options list. Redshift
// inherits the PostgreSQL grammar.
func (p *Parser) isExplainParenOptionsDialect() bool {
	switch p.Dialect() {
	case string(keywords.DialectPostgreSQL),
		string(keywords.DialectRedshift):
		return true
	}
	return false
}

// parseExplainParenOptions parses PostgreSQL's EXPLAIN options list:
//
//	( option [ value ] [, ...] )
//
// ANALYZE and FORMAT map onto the AST; every other option of that shape
// (VERBOSE, COSTS, BUFFERS, WAL, TIMING, SUMMARY, ...) is consumed and
// discarded — the modelled pair is all the AST carries, and accepting
// the rest by shape keeps the parser stable as PostgreSQL grows options
// (the server rejects genuinely unknown ones on its side).
//
// The caller has verified the current token is LPAREN and the dialect
// accepts the form.
//
// Deliberate superset notes: the tokenizer strips quotes, so
// FORMAT 'json' is accepted where PostgreSQL requires an unquoted
// word; YES/NO are accepted as booleans alongside PostgreSQL's
// TRUE/FALSE/ON/OFF/1/0; option VALUES are not validated against the
// option (FORMAT SELECT yields Format="SELECT"). The server-side
// grammar remains the authority on validity — this parser's job is a
// faithful AST for well-formed input and a loud error for
// structurally broken input.
func (p *Parser) parseExplainParenOptions() (analyze bool, format string, err error) {
	p.advance() // consume (
	if p.isType(models.TokenTypeRParen) {
		return false, "", p.expectedError("EXPLAIN option name, not an empty options list")
	}
	for {
		if !isWordValue(p.currentToken.Token.Value) {
			return analyze, format, p.expectedError("EXPLAIN option name")
		}
		name := strings.ToUpper(p.currentToken.Token.Value)
		p.advance()

		switch name {
		case "ANALYZE", "ANALYSE":
			val := true
			if !p.isType(models.TokenTypeComma) && !p.isType(models.TokenTypeRParen) {
				val, err = p.parseExplainBoolOption(name)
				if err != nil {
					return analyze, format, err
				}
			}
			analyze = val
		case "FORMAT":
			if !isWordValue(p.currentToken.Token.Value) {
				return analyze, format, p.expectedError("format identifier (TEXT, XML, JSON, YAML) after FORMAT")
			}
			format = strings.ToUpper(p.currentToken.Token.Value)
			p.advance()
		default:
			// Unmodelled option: consume its optional single-token value.
			if !p.isType(models.TokenTypeComma) && !p.isType(models.TokenTypeRParen) {
				if p.currentToken.Token.Value == "" {
					return analyze, format, p.expectedError("EXPLAIN option value, ',' or ')'")
				}
				p.advance()
			}
		}

		if p.isType(models.TokenTypeComma) {
			p.advance()
			continue
		}
		if p.isType(models.TokenTypeRParen) {
			p.advance()
			return analyze, format, nil
		}
		return analyze, format, p.expectedError("',' or ')' in EXPLAIN options list")
	}
}

// parseExplainBoolOption parses the boolean value of a parenthesised
// EXPLAIN option, accepting PostgreSQL's spellings.
func (p *Parser) parseExplainBoolOption(option string) (bool, error) {
	switch strings.ToUpper(p.currentToken.Token.Value) {
	case "TRUE", "ON", "1", "YES":
		p.advance()
		return true, nil
	case "FALSE", "OFF", "0", "NO":
		p.advance()
		return false, nil
	}
	return false, p.expectedError("boolean value for EXPLAIN option " + option)
}

// parseClickHouseExplainMode recognises ClickHouse's EXPLAIN modifier
// (AST | SYNTAX | PLAN | PIPELINE | ESTIMATE | QUERY TREE) and consumes
// the optional bare settings list that may follow it. Returns "" when
// the next tokens are not a modifier — plain EXPLAIN <stmt> stays valid
// under ClickHouse.
func (p *Parser) parseClickHouseExplainMode() (string, error) {
	switch {
	case p.isTokenMatch("PLAN"),
		p.isTokenMatch("PIPELINE"),
		p.isTokenMatch("SYNTAX"),
		p.isTokenMatch("ESTIMATE"),
		p.isTokenMatch("AST"):
		mode := strings.ToUpper(p.currentToken.Token.Value)
		p.advance()
		return mode, p.consumeClickHouseExplainSettings()
	case p.isTokenMatch("QUERY"):
		// Two-token modifier QUERY TREE. A lone QUERY is not a modifier
		// (nor a valid inner start) — leave it for the error downstream.
		if strings.EqualFold(p.peekToken().Token.Value, "TREE") {
			p.advance()
			p.advance()
			return "QUERY TREE", p.consumeClickHouseExplainSettings()
		}
	}
	return "", nil
}

// consumeClickHouseExplainSettings consumes the optional bare settings
// list (name = value [, ...]) between a ClickHouse EXPLAIN modifier and
// the inner statement. Settings are consumed and discarded so the AST
// stays dialect-neutral; callers that need them can extend the AST
// later. Discard-by-shape means a token sequence like FORMAT=JSON in
// settings position is also consumed as a setting — not mapped onto
// the Format field — which matches ClickHouse, where FORMAT is a
// trailing output clause, not a pre-statement option.
func (p *Parser) consumeClickHouseExplainSettings() error {
	for p.isIdentifier() && p.peekToken().Token.Type == models.TokenTypeEq {
		p.advance() // setting name
		p.advance() // =
		if p.currentToken.Token.Value == "" || p.isType(models.TokenTypeComma) {
			return p.expectedError("value for EXPLAIN setting")
		}
		p.advance() // value
		if p.isType(models.TokenTypeComma) {
			p.advance()
			// A comma commits to another setting; trailing commas are
			// not silently swallowed into the inner statement.
			if !p.isIdentifier() || p.peekToken().Token.Type != models.TokenTypeEq {
				return p.expectedError("another EXPLAIN setting after ','")
			}
		}
	}
	return nil
}

// isWordValue reports whether s looks like a bare word (an option or
// format name): letters only at the first character is enough to tell
// words apart from punctuation/EOF token values.
func isWordValue(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
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
