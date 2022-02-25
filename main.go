package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"os"

	"golang.org/x/tools/go/packages"
)

func main() {
	cfg := &packages.Config{Mode: packages.LoadSyntax}
	pkgs, err := packages.Load(cfg, os.Args[1:]...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}
	if packages.PrintErrors(pkgs) > 0 {
		os.Exit(1)
	}

	// Collect Go Track1 function declarations that returns
	for _, pkg := range pkgs {
		for _, f := range pkg.Syntax {
			ast.Inspect(f, func(node ast.Node) bool {
				asnmt, ok := node.(*ast.AssignStmt)
				if !ok {
					return true
				}
				if len(asnmt.Lhs) != 2 || len(asnmt.Rhs) != 1 {
					return true
				}

				ret1, ok := asnmt.Lhs[0].(*ast.Ident)
				if !ok {
					return true
				}
				if ret1.Name != "_" {
					return true
				}

				callExpr, ok := asnmt.Rhs[0].(*ast.CallExpr)
				if !ok {
					return true
				}
				selExpr, ok := callExpr.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				funcObj := pkg.TypesInfo.Uses[selExpr.Sel]
				signature, ok := funcObj.Type().(*types.Signature)
				if !ok {
					return true
				}
				res, ok := signature.Results().At(0).Type().(*types.Named)
				if !ok {
					return true
				}
				ures, ok := res.Underlying().(*types.Struct)
				if !ok {
					return true
				}
				futureType, ok := ures.Field(0).Type().(*types.Named)
				if !ok {
					return true
				}
				future := futureType.Obj()
				if future.Name() != "FutureAPI" || future.Pkg().Path() != "github.com/Azure/go-autorest/autorest/azure" {
					return true
				}
				fmt.Println(pkg.Fset.Position(asnmt.Pos()))
				return false
			})
		}
	}
}
