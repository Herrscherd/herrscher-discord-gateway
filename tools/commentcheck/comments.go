package main

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

func strayComments(files *token.FileSet, path string, file *ast.File, namedByAGenerator bool) []finding {
	if namedByAGenerator && ast.IsGenerated(file) {
		return nil
	}
	allowed := allowedDocs(file)
	if strings.HasSuffix(path, "_test.go") {
		allowed = exampleDocs(file)
		allowExampleOutputs(file, allowed)
	}
	bodies := functionBodies(file)
	directory := filepath.Dir(path)
	var found []finding
	for _, group := range file.Comments {
		if suppressions := suppressionDirectives(files, group); len(suppressions) > 0 {
			found = append(found, suppressions...)
			continue
		}
		switch {
		case isInside(group, bodies):
			if !allowed[group] {
				found = append(found, at(files, group.Pos(), refusedReason))
			}
		case allowed[group]:
			found = append(found, strayDirectiveLines(files, directory, group)...)
		case !isDirective(directory, group):
			found = append(found, at(files, group.Pos(), refusedReason))
		}
	}
	return found
}

func suppressionDirectives(files *token.FileSet, group *ast.CommentGroup) []finding {
	var found []finding
	for _, line := range group.List {
		if isSuppressionDirective(line.Text) {
			found = append(found, at(files, line.Slash, suppressionReason))
		}
	}
	return found
}

func isSuppressionDirective(text string) bool {
	body, found := strings.CutPrefix(text, "//")
	if !found {
		body, found = strings.CutPrefix(text, "/*")
	}
	if !found {
		return false
	}
	body = strings.TrimLeftFunc(body, unicode.IsSpace)
	prefixes := []string{"nolint", "lint:ignore", "lint:file-ignore"}
	return slices.ContainsFunc(prefixes, func(prefix string) bool {
		return strings.HasPrefix(body, prefix)
	})
}

func strayDirectiveLines(files *token.FileSet, directory string, group *ast.CommentGroup) []finding {
	var found []finding
	for _, line := range group.List {
		if strings.HasPrefix(line.Text, "//go:") && !isKnownDirective(directory, line.Text) {
			found = append(found, at(files, line.Slash, refusedReason))
		}
	}
	return found
}

func at(files *token.FileSet, pos token.Pos, reason string) finding {
	return finding{position: files.Position(pos), reason: reason}
}

func functionBodies(file *ast.File) []*ast.BlockStmt {
	var bodies []*ast.BlockStmt
	ast.Inspect(file, func(node ast.Node) bool {
		switch declared := node.(type) {
		case *ast.FuncDecl:
			if declared.Body != nil {
				bodies = append(bodies, declared.Body)
			}
		case *ast.FuncLit:
			bodies = append(bodies, declared.Body)
		}
		return true
	})
	return bodies
}

func isInside(group *ast.CommentGroup, bodies []*ast.BlockStmt) bool {
	return slices.ContainsFunc(bodies, func(body *ast.BlockStmt) bool {
		return group.Pos() > body.Lbrace && group.End() < body.Rbrace
	})
}

func isDirective(directory string, group *ast.CommentGroup) bool {
	return !slices.ContainsFunc(group.List, func(line *ast.Comment) bool {
		return !isKnownDirective(directory, line.Text)
	})
}
