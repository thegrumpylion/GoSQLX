package parser

import (
	"testing"

	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
)

func TestGroupingFunctionParsing(t *testing.T) {
	tree := parseSQL(t, "SELECT service_name, GROUPING(service_name) AS grp FROM spans GROUP BY ROLLUP(service_name)")
	if len(tree.Statements) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(tree.Statements))
	}
	sel, ok := tree.Statements[0].(*ast.SelectStatement)
	if !ok {
		t.Fatalf("expected SelectStatement, got %T", tree.Statements[0])
	}
	// Second column should be AliasedExpression wrapping GroupingFunction.
	if len(sel.Columns) < 2 {
		t.Fatalf("expected at least 2 columns, got %d", len(sel.Columns))
	}
	aliased, ok := sel.Columns[1].(*ast.AliasedExpression)
	if !ok {
		t.Fatalf("expected AliasedExpression, got %T", sel.Columns[1])
	}
	gf, ok := aliased.Expr.(*ast.GroupingFunction)
	if !ok {
		t.Fatalf("expected GroupingFunction, got %T", aliased.Expr)
	}
	if len(gf.Args) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(gf.Args))
	}
	ident, ok := gf.Args[0].(*ast.Identifier)
	if !ok {
		t.Fatalf("expected Identifier arg, got %T", gf.Args[0])
	}
	if ident.Name != "service_name" {
		t.Errorf("expected arg name 'service_name', got %q", ident.Name)
	}
	t.Logf("SQL roundtrip: %s", gf.SQL())
}

func TestGroupingFunctionMultipleArgs(t *testing.T) {
	tree := parseSQL(t, "SELECT GROUPING(a, b) FROM t GROUP BY CUBE(a, b)")
	sel := tree.Statements[0].(*ast.SelectStatement)
	gf, ok := sel.Columns[0].(*ast.GroupingFunction)
	if !ok {
		t.Fatalf("expected GroupingFunction, got %T", sel.Columns[0])
	}
	if len(gf.Args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(gf.Args))
	}
}
