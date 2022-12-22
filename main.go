package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

func main() {
	cfg := &packages.Config{Mode: packages.LoadSyntax}

	var inScopePkgPaths []string

	pwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}

	pkgPreFix := "github.com/hashicorp/terraform-provider-azurerm/internal/services/"

	// Instead of using `packages.Load(cfg, os.Args[1:]...)` but iterate in-scope pkg independently because:
	// 1. https://github.com/magodo/terraform-provider-azurerm-lro/issues/1
	// 2. using ./... introduces lots of non-business related pkgs, which increases unnecessary program running time
	filepath.WalkDir(pwd, func(path string, di fs.DirEntry, err error) error {
		if di.IsDir() {
			pathSeg := strings.Split(path, string(filepath.Separator))

			// Only regard top folder (e.g. network) underneath "terraform-provider-azurerm/internal/services" as in scope (e.g. exclude network/client or network/path)
			if len(pathSeg) > 0 && pathSeg[len(pathSeg)-2] == "services" {

				// Skip scanning keyvault for it fails to be handled by packages.Load(). Filing an issue https://github.com/magodo/terraform-provider-azurerm-lro/issues/1 to track.
				if di.Name() != "keyvault" {
					inScopePkgPaths = append(inScopePkgPaths, pkgPreFix+di.Name())
				}
			}
		}

		return nil
	})

	pkgs, err := packages.Load(cfg, inScopePkgPaths...)

	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}
	if packages.PrintErrors(pkgs) > 0 {
		os.Exit(1)
	}

	/*
		There are pkgs, e.g. the current COMPUTE pkg, that use both T1 and Pandora SDK, so scan both of them.
		Scanning both SDKs within one pkg introduces duplicate AST traversal, while optimizing that is yet done.
	*/
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

			fmt.Println("Track 1 Hit: " + strings.TrimPrefix(pkg.Fset.Position(asnmt.Pos()).String(), rootPath+string(filepath.Separator)))

			return false
		})
	}
}

func pandoraSDKScan(pkg *packages.Package, rootPath string) {
	funcScanCache := make(map[string][]string)

	for _, f := range pkg.Syntax {
		ast.Inspect(f, func(node ast.Node) bool {
			asnmt, ok := node.(*ast.AssignStmt)
			if !ok {
				return true
			}

			if len(asnmt.Rhs) != 1 {
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

			selExprName := selExpr.Sel.Name

			if !strings.Contains(selExprName, "Create") &&
				!strings.Contains(selExprName, "CreateOrUpdate") &&
				!strings.Contains(selExprName, "Update") &&
				!strings.Contains(selExprName, "Delete") {
				return true
			}

			// "ThenPoll being called implies the call is always correct."
			if strings.HasSuffix(selExprName, "ThenPoll") {
				return true
			}

			funcObj := pkg.TypesInfo.Uses[selExpr.Sel]

			_, ok = funcObj.Type().(*types.Signature)
			if !ok {
				return true
			}

			signature, ok := funcObj.Type().(*types.Signature)
			if !ok {
				return true
			}

			recv := signature.Recv()
			if recv == nil {
				return true
			}

			if recv.Type() == nil {
				return true
			}

			funcRecvType := signature.Recv().Type().String()
			funcPkg := funcObj.Pkg()

			// Use cache to reduce duplicate ast traversal
			if len(funcScanCache[funcRecvType]) > 0 {
				pollFuncList := funcScanCache[funcRecvType]
				for _, v := range pollFuncList {
					if selExpr.Sel.Name+"ThenPoll" == v {
						fmt.Println("Pandora Hit: " + strings.TrimPrefix(pkg.Fset.Position(asnmt.Pos()).String(), rootPath+string(filepath.Separator)))
						break
					}
				}

				return false
			}

			// Get Pandora SDK package containing the being called synchronized CRUD function, and verify whether there is also `ThenPoll()` function.
			// If there is, then it's bug.
			cfgInner := &packages.Config{Mode: packages.LoadSyntax}
			pkgsInner, err := packages.Load(cfgInner, funcPkg.Path())
			if err != nil {
				fmt.Fprintf(os.Stderr, "load: %v\n", err)
				os.Exit(1)
			}
			if packages.PrintErrors(pkgsInner) > 0 {
				os.Exit(1)
			}

			for _, pkgInner := range pkgsInner {
				for _, f := range pkgInner.Syntax {
					ast.Inspect(f, func(node ast.Node) bool {
						funcDecl, ok := node.(*ast.FuncDecl)
						if !ok {
							return true
						}

						// CRUD and CRUDThenPoll function must have receiver in Pandora SDK, otherwise continue the ast iteration.
						if funcDecl.Recv == nil {
							return true
						}

						funcDeclRecvList := funcDecl.Recv.List
						if funcDeclRecvList != nil && len(funcDeclRecvList) > 0 {
							funcDeclName := funcDecl.Name.Name
							if !strings.Contains(funcDeclName, "Create") && !strings.Contains(funcDeclName, "Update") && !strings.Contains(funcDeclName, "Delete") {
								return true
							}

							if funcDeclRecvList[len(funcDeclRecvList)-1] == nil {
								return true
							}

							recvType, ok := funcDeclRecvList[len(funcDeclRecvList)-1].Type.(*ast.Ident)
							if !ok {
								return true
							}

							/*
								1. Cannot only use func name Create/Update/Delete to identify the unique function but need to add its pkg and receiver.
								(because there could be cases where unique receiver + func name exist in multiple pkgs, and they represent different services.)
								2. Record which pkg + receiver has CRUD(ThenPoll) function
							*/
							funcScanCache[pkgInner.PkgPath+"."+recvType.Name] = append(funcScanCache[pkgInner.PkgPath+"."+recvType.Name], funcDeclName)
						}
						return false
					})
				}
			}

			if len(funcScanCache[funcRecvType]) > 0 {
				/* Here is the pkg logic this program leverages: [CRUD pkg] funcRecvType == [CRUDThenPoll Pkg] pkgInner.PkgPath+"."+recvType.Name
				e.g. [CRUD pkg] funcRecvType: 			  github.com/hashicorp/go-azure-sdk/resource-manager/fluidrelay/2022-05-26/fluidrelayservers.FluidRelayServersClient
				     [CRUDThenPoll Pkg] pkgInner.PkgPath: github.com/hashicorp/go-azure-sdk/resource-manager/fluidrelay/2022-05-26/fluidrelayservers
					 [CRUDThenPoll Pkg] recvType.Name: 	  FluidRelayServersClient
				*/

				pollFuncList := funcScanCache[funcRecvType]
				for _, v := range pollFuncList {
					// Find the case that CRUD() is called but its pandora SDK also contains CRUDThenPoll()
					if selExpr.Sel.Name+"ThenPoll" == v {

						falseAlert := ""

						// There could be false alerts because though CRUD() is called, and they have CRUDThenPoll counterparts, there are async handling right after the sync CRUD(),
						// which makes the func call still correct. Add below workaround to label possible false alerts but there might be mis-labeled ones.
						if len(asnmt.Lhs) > 1 {
							futureName, ok := asnmt.Lhs[0].(*ast.Ident)
							if ok && strings.Contains(strings.ToLower(futureName.Name), "future") {
								falseAlert = "[Likely False Alert]"
							}
						}

						fmt.Printf("%sPandora Hit: %s\n", falseAlert, strings.TrimPrefix(pkg.Fset.Position(asnmt.Pos()).String(), rootPath+string(filepath.Separator)))
					}
				}
			}

			return false
		})
	}
}
