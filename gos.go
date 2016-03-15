package main

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode"
)

// dirpath must be absolute
func Rewrite(dirpath string) {
	table := Table([]*Row{})

	// add one row for each file
	{
		fileInfos, err := ioutil.ReadDir(dirpath)
		if err != nil {
			panic(err)
		}
		filepaths := []string{}
		for _, fi := range fileInfos {
			abspath := filepath.Join(dirpath, fi.Name())
			if strings.HasSuffix(abspath, ".gos") {
				filepaths = append(filepaths, abspath)
			}
		}
		for _, fp := range filepaths {
			table = append(table, &Row{Filepath: fp})
		}
	}

	// read files
	table.For(func(r *Row) {
		bs, err := ioutil.ReadFile(r.Filepath)
		if err != nil {
			panic(err)
		}
		r.Content = string(bs)
	})

	{
		// create file set from files
		fset := token.NewFileSet()
		table.For(func(r *Row) {
			r.Fset = fset
			fset.AddFile(r.Filepath, -1, len(r.Content))
		})
	}

	// parse files
	table.For(func(r *Row) {
		ast, err := parser.ParseFile(r.Fset, r.Filepath, nil, 0)
		if err != nil {
			panic(err)
		}
		r.Ast = ast
	})

	// find async and async(E) positions
	table.For(func(r *Row) {
		r.AsyncLocations = map[Location]Ignored{}
		r.AsyncELocations = map[Location]Ignored{}

		// get all objects O such that there exists an ID := async(E) and ID points to O
		ast.Inspect(r.Ast, func(n ast.Node) bool {
			if n != nil {
				if shortDecl, ok := n.(*ast.AssignStmt); ok && shortDecl != nil {
					if shortDecl.Tok == token.DEFINE {
						if len(shortDecl.Rhs) == 1 {
							if call, ok := shortDecl.Rhs[0].(*ast.CallExpr); ok && call != nil {
								if funcId, ok := call.Fun.(*ast.Ident); ok && funcId != nil {
									if funcId.Name == "async" {
										E := call.Args[0]                              // the E in async(E)
										r.AsyncLocations[Node2Loc(funcId, r.Fset)] = 0 // the async in async(E)
										r.AsyncELocations[Node2Loc(E, r.Fset)] = 0     // the E in async(E)
									}
								}
							}
						}
					}
				}
			}
			return true
		})
	})

	// replace "async" function expressions with "     "
	table.For(func(r *Row) {
		r.AsyncReplacements = []Replacement{}
		for loc := range r.AsyncLocations {
			r.AsyncReplacements = append(r.AsyncReplacements, Replacement{loc, "     "})
		}
		r.AsyncReplaced = Replace(r.Content, r.AsyncReplacements)
	})

	table.For(func(r *Row) {
		r.AsyncReplacedFilepath = r.Filepath + ".async_replaced"
		err := ioutil.WriteFile(r.AsyncReplacedFilepath, []byte(r.AsyncReplaced), FilePerm)
		if err != nil {
			panic(err)
		}
	})

	{
		fset := token.NewFileSet()
		table.For(func(r *Row) {
			fset.AddFile(r.AsyncReplacedFilepath, -1, len(r.AsyncReplaced))
			r.Fset2 = fset
		})
	}

	table.For(func(r *Row) {
		ast, err := parser.ParseFile(r.Fset2, r.AsyncReplacedFilepath, nil, 0)
		if err != nil {
			panic(err)
		}
		r.AsyncReplacedAst = ast
	})

	{
		// Type-check the package.
		// We create an empty map for each kind of input
		// we're interested in, and Check populates them.
		info := &types.Info{
			Types: make(map[ast.Expr]types.TypeAndValue),
		}
		// collect async replaced asts
		asts := []*ast.File{}
		table.For(func(r *Row) {
			asts = append(asts, r.AsyncReplacedAst)
		})
		fset := table[0].Fset2
		conf := types.Config{Importer: importer.For("gc", nil)}
		pkg, err := conf.Check("I think this is doesn't matter?", fset, asts, info)
		for _, imp := range pkg.Imports() {
			fullImportPath, isUserPkg := Expand(imp.Path())
			if isUserPkg {
				Rewrite(fullImportPath)
			}
		}

		if err != nil {
			panic(err)
		}

		// assign info to rows
		table.For(func(r *Row) {
			r.Info = info
		})
	}

	table.For(func(r *Row) {
		// generate rewrites for E in async(E)
		r.AsyncEReplacements = []Replacement{}
		for expr, tv := range r.Info.Types {
			exprStart := r.Fset2.Position(expr.Pos())
			exprEnd := r.Fset2.Position(expr.End())
			exprStart.Filename = r.Filepath
			exprEnd.Filename = r.Filepath
			loc := Location{exprStart, exprEnd}
			for eloc := range r.AsyncELocations {
				if loc == eloc {
					// generate a rewrite for eloc
					typs := getTypes(tv)
					exprStr := r.Content[loc.Start.Offset:loc.End.Offset]
					r.AsyncEReplacements = append(r.AsyncEReplacements,
						Replacement{eloc, genAsync(typs, exprStr)},
					)
					break
				}
			}
		}
	})

	// YOLO
	table.For(func(r *Row) {
		r.WE_Filepath = r.Filepath + ".wrap_expression"
		r.WE_Content = Replace(r.Content, r.AsyncEReplacements)
		err := ioutil.WriteFile(r.WE_Filepath,
			[]byte(r.WE_Content), FilePerm)
		if err != nil {
			panic(err)
		}

	})
	{
		fset := token.NewFileSet()
		table.For(func(r *Row) {
			fset.AddFile(r.WE_Filepath, -1, len(r.WE_Content))
			r.WE_Fset = fset
		})
	}
	table.For(func(r *Row) {
		ast, err := parser.ParseFile(r.WE_Fset, r.WE_Filepath, nil, 0)
		if err != nil {
			panic(err)
		}
		r.WE_Ast = ast
	})

	table.For(func(r *Row) {
		r.IdsDontReplace = map[*ast.Ident]Ignored{}
		r.ShortDecls = map[*ast.AssignStmt]Ignored{}
		r.AsyncLocations2 = map[Location]Ignored{}
		ast.Inspect(r.WE_Ast, func(n ast.Node) bool {
			if n != nil {
				if shortDecl, ok := n.(*ast.AssignStmt); ok && shortDecl != nil {
					if shortDecl.Tok == token.DEFINE {
						if len(shortDecl.Rhs) == 1 {
							if call, ok := shortDecl.Rhs[0].(*ast.CallExpr); ok && call != nil {
								if funcId, ok := call.Fun.(*ast.Ident); ok && funcId != nil {
									if funcId.Name == "async" {
										r.AsyncLocations2[Node2Loc(funcId, r.WE_Fset)] = 0
										// iterate through LHS of "id0, id1, ... := async(E)"
										r.ShortDecls[shortDecl] = 0
										for _, lhs := range shortDecl.Lhs {
											id := lhs.(*ast.Ident)
											r.IdsDontReplace[id] = 0
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

		r.IdReplacements = []Replacement{}
		ast.Inspect(r.WE_Ast, func(n ast.Node) bool {
			if n != nil {
				if id, ok := n.(*ast.Ident); ok && id != nil { // get all IDs
					if _, ok := r.IdsDontReplace[id]; !ok { // that aren't ID := async(E)
						if id.Obj != nil && id.Obj.Decl != nil {
							if assignStmt, ok := id.Obj.Decl.(*ast.AssignStmt); ok {
								// that have point to obj declared in decls
								if _, ok := r.ShortDecls[assignStmt]; ok {
									loc := Location{
										r.WE_Fset.Position(id.Pos()),
										r.WE_Fset.Position(id.End()),
									}
									r.IdReplacements = append(r.IdReplacements,
										Replacement{loc, id.Name + "()"})
								}
							}
						}
					}
				}
			}
			return true
		})
	})

	// replace "async" function expressions with "     "
	table.For(func(r *Row) {
		r.AsyncReplacements2 = []Replacement{}
		for loc := range r.AsyncLocations2 {
			r.AsyncReplacements2 = append(r.AsyncReplacements2, Replacement{loc, "     "})
		}
	})

	// apply id rewrites and write .go files
	table.For(func(r *Row) {
		r.FinalContent = Replace(r.WE_Content, append(r.IdReplacements, r.AsyncReplacements2...))
		newFilepath := strings.TrimSuffix(r.Filepath, "gos") + "go"
		err := ioutil.WriteFile(newFilepath, []byte(r.FinalContent), FilePerm)
		if err != nil {
			panic(err)
		}
	})

	// clean up temp files
	table.For(func(r *Row) {
		err := os.Remove(r.AsyncReplacedFilepath)
		if err != nil {
			panic(err)
		}
		err = os.Remove(r.WE_Filepath)
		if err != nil {
			panic(err)
		}
	})
}

const FilePerm os.FileMode = 0644

type Ignored int

type Location struct {
	Start token.Position
	End   token.Position
}

func Node2Loc(n ast.Node, fset *token.FileSet) Location {
	return Location{
		fset.Position(n.Pos()),
		fset.Position(n.End()),
	}
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

type Replacement struct {
	Location
	New string
}

// Expand converts a Go package import path to an absolute directory path.
// Returns true if found in GOPATH, false if found in GOROOT.
func Expand(path string) (string, bool) {
	// try GOROOT
	goroot := filepath.Join(runtime.GOROOT(), "src", path)
	if IsDir(goroot) {
		return goroot, false
	}

	// try GOPATH
	gopath := filepath.Join(os.Getenv("GOPATH"), "src", path)
	if IsDir(gopath) {
		return gopath, true
	}

	panic("Expand: neither GOROOT nor GOPATH works")
}

func IsDir(path string) bool {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fileInfo.IsDir()
}

// assumes replacements has no duplicates
func Replace(original string, replacements []Replacement) string {
	// sort replacments by r.Location.Start
	// scan through replacements and get non replacements
	// interleave replacements and non replacements
	sort.Sort(ByStart(replacements))
	rewrite := ""
	prev := 0
	for _, replacement := range replacements {
		rewrite += original[prev:replacement.Location.Start.Offset]
		rewrite += replacement.New
		prev = replacement.Location.End.Offset
	}
	rewrite += original[prev:]
	return rewrite
}

type ByStart []Replacement

func (s ByStart) Len() int           { return len(s) }
func (s ByStart) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s ByStart) Less(i, j int) bool { return s[i].Location.Start.Offset < s[j].Location.Start.Offset }

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
