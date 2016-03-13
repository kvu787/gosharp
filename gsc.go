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
	"sort"
	"strings"
	"unicode"
)

const FilePerm os.FileMode = 0644

var usage = string(`usage: ./gsc <.gos files>
output: .go files that comprise a Go package.
`)

type Table []*Row

func (t Table) For(f func(r *Row)) {
	for _, r := range t {
		f(r)
	}
}

type Ignored int

type Row struct {
	Filepath              string
	Content               string
	Fset                  *token.FileSet // shared
	Ast                   *ast.File
	AsyncLocations        map[Location]Ignored
	AsyncELocations       map[Location]Ignored
	AsyncReplacedFilepath string
	AsyncReplacements     []Replacement
	AsyncReplaced         string
	Fset2                 *token.FileSet // shared
	AsyncReplacedAst      *ast.File
	Info                  *types.Info // typechecking info
	AsyncEReplacements    []Replacement
	IdsDontRewrite        map[*ast.Ident]Ignored      // ids in ID := async(E)
	ShortDecls            map[*ast.AssignStmt]Ignored // ast nodes of (ID := async(E))
	IdReplacements        []Replacement
	FinalContent          string
}

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

func main() {

	if len(os.Args) == 1 || os.Args[1] == "-h" || os.Args[1] == "--help" {
		fmt.Println(usage)
		os.Exit(0)
	}
	filepaths := os.Args[1:]
	table := Table([]*Row{})
	for _, filepath := range filepaths {
		table = append(table, &Row{Filepath: filepath})
	}

	table.For(func(r *Row) {
		bs, err := ioutil.ReadFile(r.Filepath)
		if err != nil {
			panic(err)
		}
		r.Content = string(bs)
	})

	{
		fset := token.NewFileSet()
		table.For(func(r *Row) {
			r.Fset = fset
			fset.AddFile(r.Filepath, -1, len(r.Content))
		})
	}
	table.For(func(r *Row) {
		ast, err := parser.ParseFile(r.Fset, r.Filepath, nil, 0)
		if err != nil {
			panic(err)
		}
		r.Ast = ast
	})

	table.For(func(r *Row) {
		// find async and async(E) positions
		r.AsyncLocations = map[Location]Ignored{}
		r.AsyncELocations = map[Location]Ignored{}
		ast.Inspect(r.Ast, func(n ast.Node) bool {
			// get all objects O that there exists an ID := async(E) and ID points
			// to O
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
	table.For(func(r *Row) {
		r.AsyncReplacements = []Replacement{}
		for loc := range r.AsyncLocations {
			r.AsyncReplacements = append(r.AsyncReplacements, Replacement{loc, "     "})
		}
		r.AsyncReplaced = Rewrite(r.Content, r.AsyncReplacements)
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
			Defs:  make(map[*ast.Ident]types.Object),
			Uses:  make(map[*ast.Ident]types.Object),
		}
		// collect async replaced asts
		asts := []*ast.File{}
		table.For(func(r *Row) {
			asts = append(asts, r.AsyncReplacedAst)
		})
		fset := table[0].Fset2
		conf := types.Config{Importer: importer.Default()}
		_, err := conf.Check("I think this is doesn't matter?", fset, asts, info)
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

	table.For(func(r *Row) {
		r.IdsDontRewrite = map[*ast.Ident]Ignored{}
		r.ShortDecls = map[*ast.AssignStmt]Ignored{}
		ast.Inspect(r.Ast, func(n ast.Node) bool {
			if n != nil {
				if shortDecl, ok := n.(*ast.AssignStmt); ok && shortDecl != nil {
					if shortDecl.Tok == token.DEFINE {
						if len(shortDecl.Rhs) == 1 {
							if call, ok := shortDecl.Rhs[0].(*ast.CallExpr); ok && call != nil {
								if funcId, ok := call.Fun.(*ast.Ident); ok && funcId != nil {
									if funcId.Name == "async" {
										// iterate through LHS of "id0, id1, ... := async(E)"
										r.ShortDecls[shortDecl] = 0
										for _, lhs := range shortDecl.Lhs {
											id := lhs.(*ast.Ident)
											r.IdsDontRewrite[id] = 0
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
		ast.Inspect(r.Ast, func(n ast.Node) bool {
			if n != nil {
				if id, ok := n.(*ast.Ident); ok && id != nil { // get all IDs
					if _, ok := r.IdsDontRewrite[id]; !ok { // that aren't ID := async(E)
						if id.Obj != nil && id.Obj.Decl != nil {
							if assignStmt, ok := id.Obj.Decl.(*ast.AssignStmt); ok {
								// that have point to obj declared in decls
								if _, ok := r.ShortDecls[assignStmt]; ok {
									loc := Location{
										r.Fset.Position(id.Pos()),
										r.Fset.Position(id.End()),
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

	table.For(func(r *Row) {
		// apply all replacements
		r.FinalContent = Rewrite(r.Content, append(r.AsyncReplacements,
			append(r.AsyncEReplacements, r.IdReplacements...)...))
		// write final files
		err := ioutil.WriteFile(
			strings.TrimSuffix(r.Filepath, "gos")+"go",
			[]byte(r.FinalContent),
			FilePerm,
		)
		if err != nil {
			panic(err)
		}
	})

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

// assumes replacements has no duplicates
func Rewrite(original string, replacements []Replacement) string {
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
