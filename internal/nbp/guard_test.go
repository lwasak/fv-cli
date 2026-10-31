package nbp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestGuard_NoFloat(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "math" {
				t.Errorf("%s imports math", name)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if x.Name == "float32" || x.Name == "float64" {
					t.Errorf("%s: %s uses %s", name, fset.Position(x.Pos()), x.Name)
				}
			case *ast.SelectorExpr:
				if x.Sel.Name == "ParseFloat" || x.Sel.Name == "Float64" {
					t.Errorf("%s: %s uses %s", name, fset.Position(x.Pos()), x.Sel.Name)
				}
			case *ast.BasicLit:
				if x.Kind == token.FLOAT {
					t.Errorf("%s: %s float literal", name, fset.Position(x.Pos()))
				}
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no non-test sources checked")
	}
}
