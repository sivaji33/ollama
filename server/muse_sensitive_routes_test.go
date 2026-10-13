package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

func TestMUSEAgentRoutesRequireAuth(t *testing.T) {
	file, err := parser.ParseFile(
		token.NewFileSet(),
		"routes.go",
		nil,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}

	required := map[string]bool{
		"/api/agent/run":                   false,
		"/api/agent/context":               false,
		"/api/agent/approvals":             false,
		"/api/agent/approvals/:id":         false,
		"/api/agent/session":               false,
		"/api/agent/session/:id/continue":  false,
		"/api/agent/session/:id/cancel":    false,
		"/api/agent/session/:id":           false,
		"/api/agent/session/:id/events":    false,
		"/api/agent/session/:id/diff":      false,
		"/api/self-development":            false,
		"/api/self-development/:id":        false,
		"/api/self-development/:id/cancel": false,
	}

	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		receiver, ok := selector.X.(*ast.Ident)
		if !ok || receiver.Name != "r" {
			return true
		}

		switch selector.Sel.Name {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "Any":
		default:
			return true
		}

		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok {
			return true
		}

		path, err := strconv.Unquote(literal.Value)
		if err != nil {
			return true
		}

		if !strings.HasPrefix(path, "/api/agent/") &&
			!strings.HasPrefix(path, "/api/self-development") {
			return true
		}

		authCount := 0

		for _, argument := range call.Args[1:] {
			middleware, ok := argument.(*ast.CallExpr)
			if !ok {
				continue
			}

			function, ok := middleware.Fun.(*ast.Ident)
			if ok && function.Name == "museApprovalAuth" {
				authCount++
			}
		}

		if authCount != 1 {
			t.Errorf(
				"route %s %s has %d auth guards; want 1",
				selector.Sel.Name,
				path,
				authCount,
			)
		}

		if _, exists := required[path]; exists {
			required[path] = true
		}

		return true
	})

	for path, registered := range required {
		if !registered {
			t.Errorf("required agent route missing: %s", path)
		}
	}
}
