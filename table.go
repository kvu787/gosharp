package main

import (
	"go/ast"
	"go/token"
	"go/types"
)

type Row struct {
	Filepath string

	Content         string         // original source
	Fset            *token.FileSet // shared
	Ast             *ast.File
	AsyncLocations  map[Location]Ignored
	AsyncELocations map[Location]Ignored

	AsyncReplacedFilepath string
	AsyncReplacements     []Replacement
	AsyncReplaced         string
	Fset2                 *token.FileSet // shared
	AsyncReplacedAst      *ast.File
	Info                  *types.Info // typechecking info

	AsyncEReplacements []Replacement
	WE_Filepath        string
	WE_Content         string
	WE_Replacements    []Replacement
	WE_Fset            *token.FileSet
	WE_Ast             *ast.File

	RI_Filepath        string
	GO_Filepath        string
	AsyncLocations2    map[Location]Ignored
	AsyncReplacements2 []Replacement
	IdsDontReplace     map[*ast.Ident]Ignored      // ids in ID := async(E)
	ShortDecls         map[*ast.AssignStmt]Ignored // ast nodes of (ID := async(E))
	IdReplacements     []Replacement
	FinalContent       string
}

type Table []*Row

func (t Table) For(f func(r *Row)) {
	for _, r := range t {
		f(r)
	}
}
