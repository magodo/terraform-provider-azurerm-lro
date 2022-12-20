package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// nanxu: THere could be false alert that code using sync CRUD but have future to accept and write customzied async

func main() {
	cfg := &packages.Config{Mode: packages.LoadSyntax}

	// nanxu Test Func
	fmt.Printf("%s: %d\n", os.Args[1], len(os.Args[1:]))
	test(os.Args[1:]...)

	pkgs, err := packages.Load(cfg, os.Args[1:]...)

	//nanxu
	//pkgs, err := packages.Load(cfg, "D:\\code\\terraform-provider-azurerm\\internal\\services\\network", "D:\\code\\terraform-provider-azurerm\\internal\\services\\eventhub")

	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}
	if packages.PrintErrors(pkgs) > 0 {
		os.Exit(1)
	}

	pwd, _ := os.Getwd()

	/*
		There are pkgs, e.g. the current COMPUTE pkg, that use both T1 and Pandora SDK, so scan both of them.
		Scanning both SDKs within one pkg introduces duplicate AST traversal, while optimizing that is yet done.
	*/
	for _, pkg := range pkgs {
		fmt.Printf("%s\n", pkg.Name)
		trackOneSDKScan(pkg, pwd)
		pandoraSDKScan(pkg, pwd)
	}
}

func test(args ...string) {
	for _, v := range args {
		fmt.Printf("%s\n", v)
	}
}

// Enable below checking logic later when one pkg uses either Track1 or Pandora, rather than both. This can help reduce duplicate AST traversal.
/*
func isTrackOnePkg(pkg *packages.Package) bool {
	ret := true
	stopAstTraverse := false

	for _, f := range pkg.Syntax {
		ast.Inspect(f, func(node ast.Node) bool {

			// With the assumption that one pkg uses either Track1 or Pandora, rather than both,
			// below iteration introduces unnecessary traversal, instead, the first occurrence of access is enough.
			// However, `ast.Inspect()` does not provide a way to escape?
			if stopAstTraverse {
				return true
			}

			asnmt, ok := node.(*ast.AssignStmt)
			if !ok {
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

			if strings.Contains(funcObj.Pkg().Path(), "/kermit/") ||
				strings.Contains(funcObj.Pkg().Path(), "/Azure/azure-sdk-for-go/") {
				fmt.Printf("Kermit/Track 1 SDK: %s\n", funcObj.Pkg().Path())
			} else {
				ret = false
			}

			stopAstTraverse = true

			return true
		})
	}

	return ret
}
*/

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

			//nanxu: There is null pointer exception after loosing the above check bar from == to Contains()

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

			// nanxu
			// fmt.Printf("RAW PKG: %s | Func: %s | Recv: %s\n", funcPkg.Name(), selExpr.Sel.Name, funcRecvType)

			// nanxu debug
			if funcRecvType == "github.com/Azure/azure-sdk-for-go/services/preview/botservice/mgmt/2021-05-01-preview/botservice.BotsClient" {
				fmt.Printf("stop: %d\n", len(funcScanCache[funcRecvType]))
			}

			// Use cache to reduce unnecessary ast traversal
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

			// nanxu
			//fmt.Printf("RAW2 PKG: %s | Func: %s | Recv: %s\n", funcPkg.Name(), selExpr.Sel.Name, funcRecvType)

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
								(there could be cases where unique receiver + func name exist in multiple pkgs, and they represent different services.)
								2. Record which pkg + receiver has CRUD(ThenPoll) function
							*/
							funcScanCache[pkgInner.PkgPath+"."+recvType.Name] = append(funcScanCache[pkgInner.PkgPath+"."+recvType.Name], funcDeclName)
						}
						return false
					})
				}
			}

			if len(funcScanCache[funcRecvType]) > 0 {
				/* Here is the trick: [CRUD pkg] Receiver Type == [CRUDThenPoll Pkg] pkgInner.PkgPath+"."+recvType.Name
				e.g. 			 [CRUD pkg] funcRecvType: github.com/hashicorp/go-azure-sdk/resource-manager/fluidrelay/2022-05-26/fluidrelayservers.FluidRelayServersClient
				     [CRUDThenPoll Pkg] pkgInner.PkgPath: github.com/hashicorp/go-azure-sdk/resource-manager/fluidrelay/2022-05-26/fluidrelayservers
						[CRUDThenPoll Pkg] recvType.Name: FluidRelayServersClient
				*/

				pollFuncList := funcScanCache[funcRecvType]
				for _, v := range pollFuncList {
					// Find the case that CRUD() is called but its pandora SDK also contains CRUDThenPoll()
					if selExpr.Sel.Name+"ThenPoll" == v {
						fmt.Println("Pandora Hit: " + strings.TrimPrefix(pkg.Fset.Position(asnmt.Pos()).String(), rootPath+string(filepath.Separator)))
					}
				}
			}

			return false
		})
	}
}
