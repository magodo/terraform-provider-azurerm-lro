package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"golang.org/x/tools/go/packages"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

func main() {
	pwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}

	cfg := &packages.Config{Mode: packages.LoadAllSyntax}

	pkgs, err := packages.Load(cfg, os.Args[1:]...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}
	if packages.PrintErrors(pkgs) > 0 {
		os.Exit(1)
	}

	for _, pkg := range pkgs {
		trackOneSDKScan(pkg, pwd)
		pandoraSDKScan(pkg, pwd)
	}
}

func trackOneSDKScan(pkg *packages.Package, rootPath string) {
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

			if pkg.TypesInfo == nil {
				return true
			}
			funcObj, ok := pkg.TypesInfo.Uses[selExpr.Sel]
			if !ok {
				return true
			}

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

			fmt.Println("Track 1 Hit: " + strings.TrimPrefix(pkg.Fset.Position(asnmt.Pos()).String(), rootPath+string(filepath.Separator)))

			return false
		})
	}
}

func pandoraSDKScan(pkg *packages.Package, rootPath string) {
	usesSet := map[types.Object][]string{}

	// Collect Uses
	for ident, def := range pkg.TypesInfo.Uses {
		if !strings.Contains(ident.Name, "Create") &&
			!strings.Contains(ident.Name, "CreateOrUpdate") &&
			!strings.Contains(ident.Name, "Update") &&
			!strings.Contains(ident.Name, "Delete") {
			continue
		}

		// Only identify those Sync calls in Uses
		if strings.Contains(ident.Name, "ThenPoll") {
			continue
		}

		// Only identify Methods in Uses
		_, ok := def.Type().(*types.Signature)
		if !ok {
			continue
		}

		usesSet[def] = append(usesSet[def], strings.TrimPrefix(pkg.Fset.Position(ident.Pos()).String(), rootPath+string(filepath.Separator)))
	}

	// Collect SDK
	var sdkPkgPathList []string
	for importedPkgPath := range pkg.Imports {
		// Only target at Pandora SDK pkg import
		if strings.Contains(importedPkgPath, "github.com/hashicorp/go-azure-sdk/resource-manager/") {
			sdkPkgPathList = append(sdkPkgPathList, importedPkgPath)
		}
	}

	if len(sdkPkgPathList) == 0 {
		return
	}

	sdkSyncSet := map[types.Object]string{}
	sdkAsyncSet := map[string]bool{}

	for _, sdkPkgPath := range sdkPkgPathList {
		if pkg.Imports[sdkPkgPath] == nil || pkg.Imports[sdkPkgPath].TypesInfo == nil {
			return
		}

		for ident, def := range pkg.Imports[sdkPkgPath].TypesInfo.Defs {
			if !strings.Contains(ident.Name, "Create") &&
				!strings.Contains(ident.Name, "CreateOrUpdate") &&
				!strings.Contains(ident.Name, "Update") &&
				!strings.Contains(ident.Name, "Delete") {
				continue
			}

			isPrivateMethod := false

			// private method is out of the scan scope
			for _, v := range ident.Name {
				if unicode.IsLower(v) {
					isPrivateMethod = true
					break
				}

				break
			}

			if isPrivateMethod {
				continue
			}

			if strings.Contains(ident.Name, "ThenPoll") {
				sdkAsyncSet[def.Type().(*types.Signature).Recv().Type().String()+"."+ident.Name] = true
			} else {
				sdkSyncSet[def] = ident.Name
			}
		}
	}

	// Identify the existence of CRUDThenPoll if they have counterpart sync CRUD in SDK and those sync CRUD are used
	// Canonical comparison is used in comparing sync SDK def and uses, while is not used in comparing sync SDK and async SDK
	// for the two def objects are different.
	for usesDef, pos := range usesSet {
		if sdkIdentName, exist := sdkSyncSet[usesDef]; exist {

			sig, ok := usesDef.Type().(*types.Signature)
			if !ok {
				continue
			}

			recv := sig.Recv()
			if recv == nil {
				continue
			}

			if sdkAsyncSet[recv.Type().String()+"."+sdkIdentName+"ThenPoll"] {
				for _, posItem := range pos {
					fmt.Printf("Pandora Hit: %s\n", posItem)
				}
			}
		}
	}
}
