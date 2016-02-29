package main

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	// "os"
	"sort"
	"strings"
	"unicode"
)

var src = string(`package main
import "fmt"
func e() (int, string) {
	return 0, "e() retval"
}
func DoLongComputation() uint64 { return 0 }
func main() {
	id, id2 := async(e())
	result := async(DoLongComputation() + 1)
	fmt.Println(id, id2, result)
}`)

func main() {
	// given *.gos files for a package
	// rewrite each file
	// output *.go files
	// filepaths := os.Args[1:]
	// asts, fset := ParseFiles(filepaths)
	// for filepath, f := range asts {
	//
	// }
	fmt.Println(Rewrite(src, append(asyncReplacements(src), idReplacements(src)...)))
}

// func AsyncRewrite(fset *token.FileSet, f *ast.File) string {
//
// }

func ParseFiles(paths []string) (map[string]*ast.File, *token.FileSet) {
	asts := map[string]*ast.File{}
	fset := token.NewFileSet()
	for _, path := range paths {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			panic(err)
		}
		asts[path] = f
	}
	return asts, fset
}

func getTypes(tv types.TypeAndValue) []string {
	typ := tv.Type
	typs := []string{}
	if tuple, ok := typ.(*types.Tuple); ok {
		for i := 0; i < tuple.Len(); i++ {
			typs = append(typs, tuple.At(i).Type().String())
		}
	} else {
		typs = append(typs, typ.String())
	}
	return typs
}

func trimComma(s string) string {
	s = strings.TrimRightFunc(s, unicode.IsSpace)
	s = strings.TrimRight(s, ",")
	return s
}

func genAsync(typs []string, expr string) string {
	rettype := ""
	for _, t := range typs {
		rettype += fmt.Sprintf("func() %v, ", t)
	}
	rettype = trimComma(rettype)

	results := ""
	for i, t := range typs {
		results += fmt.Sprintf("\tvar __result%d %v\n", i, t)
	}

	lhs := ""
	for i := range typs {
		lhs += fmt.Sprintf("__result%d, ", i)
	}
	lhs = trimComma(lhs)

	retval := ""
	for i, t := range typs {
		retval += fmt.Sprintf(`func() %s {
			<-__ready
			return __result%d
		}, `, t, i)
	}
	retval = trimComma(retval)
	return fmt.Sprintf(`func() (%v) {
%v
	__ready := make(chan interface{})
	go func() {
		%v = %v
		close(__ready)
	}()
	return %v
}()`, rettype, results, lhs, expr, retval)
}

type Chunk struct {
	async Location
	types []string
}

func asyncReplacements(src string) []Replacement {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		panic(err)
	}

	asyncs := map[Location]*Chunk{}

	// Inspect performs a preorder traversal of the AST
	ast.Inspect(f, func(n ast.Node) bool {
		// get all objects O that there exists an ID := async(E) and ID points
		// to O
		if n != nil {
			if shortDecl, ok := n.(*ast.AssignStmt); ok {
				if shortDecl.Tok == token.DEFINE {
					if len(shortDecl.Rhs) == 1 {
						if call, ok := shortDecl.Rhs[0].(*ast.CallExpr); ok {
							shortDecl.Rhs[0] = call.Args[0]
							if funId, ok := call.Fun.(*ast.Ident); ok {
								if funId.Name == "async" {
									arg := call.Args[0]
									loc := Location{
										fset.Position(arg.Pos()).Offset,
										fset.Position(arg.End()).Offset,
									}
									asyncs[loc] = &Chunk{
										async: Location{
											fset.Position(call.Pos()).Offset,
											fset.Position(call.End()).Offset,
										},
									}
								}
							}
						}
					}
				}
			}
		}
		return true
	})

	src2 := strings.Replace(src, "async", "     ", -1)
	fset2 := token.NewFileSet()
	f2, err := parser.ParseFile(fset2, "src2.go", src2, 0)
	if err != nil {
		panic(err)
	}
	// typecheck
	// Type-check the package.
	// We create an empty map for each kind of input
	// we're interested in, and Check populates them.
	info := types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}
	conf := types.Config{Importer: importer.Default()}
	_, err = conf.Check("main", fset2, []*ast.File{f2}, &info)
	if err != nil {
		panic(err)
	}
	for expr, typ := range info.Types {
		loc := Location{
			fset2.Position(expr.Pos()).Offset,
			fset2.Position(expr.End()).Offset,
		}
		if _, ok := asyncs[loc]; ok {
			asyncs[loc].types = getTypes(typ)
		}
	}

	replacements := []Replacement{}
	for eloc, chunk := range asyncs {
		expr := src[eloc.Start:eloc.End]
		replacements = append(replacements, Replacement{
			chunk.async,
			genAsync(chunk.types, expr),
		})
	}
	return replacements
}

func idReplacements(src string) []Replacement {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		panic(err)
	}

	// IDs that appear on LHS of an async declaration
	// id0, id1, ... := async(E)
	// don't rewrite these ids
	ids := map[*ast.Ident]int{}

	// async short decls
	// (id := async(E)), (a, b := async(E))
	// rewrite id to id() if has object declared in decls
	decls := map[*ast.AssignStmt]int{}

	// Inspect performs a preorder traversal of the AST
	ast.Inspect(f, func(n ast.Node) bool {
		// get all objects O that there exists an ID := async(E) and ID points
		// to O
		if n != nil {
			if shortDecl, ok := n.(*ast.AssignStmt); ok {
				if shortDecl.Tok == token.DEFINE {
					if len(shortDecl.Rhs) == 1 {
						if call, ok := shortDecl.Rhs[0].(*ast.CallExpr); ok {
							shortDecl.Rhs[0] = call.Args[0]
							if funId, ok := call.Fun.(*ast.Ident); ok {
								if funId.Name == "async" {
									// print("rewrite async", fset.Position(call.Lparen), fset.Position(call.Rparen))
									// iterate through LHS: id0, id1, ... := ...
									for _, lhs := range shortDecl.Lhs {
										id := lhs.(*ast.Ident)
										ids[id] = 0
										decls[id.Obj.Decl.(*ast.AssignStmt)] = 0
									}
								}
							}
						}
					}
				}
			}
		}
		return true
	})

	/*
		foreach obj in objects {
			get all IDs such that:
				ID points to obj
				ID is not in decl ID set
		}
	*/
	replacements := []Replacement{}
	ast.Inspect(f, func(n ast.Node) bool {
		if n != nil {
			if id, ok := n.(*ast.Ident); ok { // get all IDs
				if _, ok := ids[id]; !ok { // that aren't ID := async(E)
					// ast.Print(nil, id)
					if id.Obj != nil && id.Obj.Decl != nil {
						if assignStmt, ok := id.Obj.Decl.(*ast.AssignStmt); ok {
							// that have point to obj declared in decls
							if _, ok := decls[assignStmt]; ok {
								loc := Location{
									fset.Position(id.Pos()).Offset,
									fset.Position(id.End()).Offset,
								}
								replacements = append(replacements,
									Replacement{
										loc,
										id.Name + "()",
									})
								// fmt.Println("rewrite ID =", id.Name, loc)
							}
						}
					}
				}
			}
		}
		return true
	})
	return replacements
}

type Location struct {
	Start int
	End   int
}

type Replacement struct {
	Location
	New string
}

//        (1, 3)       (5, 7)        (20, 22)
// (0, 1)       (3, 5)        (7, 20)        (22, end)
func Rewrite(original string, replacements []Replacement) string {
	// sort replacments by r.Location.Start
	// scan through replacements and get non replacements
	// interleave replacements and non replacements
	sort.Sort(ByStart(replacements))
	rewrite := ""
	prev := 0
	for _, replacement := range replacements {
		rewrite += original[prev:replacement.Location.Start]
		rewrite += replacement.New
		prev = replacement.Location.End
	}
	rewrite += original[prev:]
	return rewrite
}

type ByStart []Replacement

func (s ByStart) Len() int           { return len(s) }
func (s ByStart) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s ByStart) Less(i, j int) bool { return s[i].Start < s[j].Start }
