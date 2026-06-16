// Compiled, allocation-light evaluation of FHIR search-parameter FHIRPath
// expressions for the indexing hot path.
//
// The generic Evaluate interpreter (eval.go) re-walks the AST node-by-node,
// allocating a fresh intermediate []any at every node (evalNode), boxing every
// scalar leaf into a 1-element slice (flatten), comparing where() operands with
// fmt.Sprintf (reflection), and re-running expandPolymorphic on every single
// evaluation. On ingest that interpreter runs ~20-40 times per resource, so
// those allocations dominate index-extraction CPU and GC.
//
// EvaluatePolymorphicCompiled compiles each distinct expression ONCE (caching
// the expanded+parsed program) and then streams matches directly into a single
// output slice — no per-node garbage, no boxing, no fmt.Sprintf for the common
// string predicates. Expressions whose AST contains a construct the fast path
// does not implement (extension(), ofType against a resourceType) transparently
// fall back to the generic node interpreter, so results are identical to
// EvaluatePolymorphic for every expression.
package fhirpath

import (
	"fmt"
	"strings"
	"sync"
)

// compiledProgram is the pre-analysed form of a search expression: the union
// chains (already expandPolymorphic-rewritten and parsed) plus whether every
// node is supported by the allocation-light evaluator.
type compiledProgram struct {
	chains   [][]node
	fastPath bool
	parseErr error
}

// compileCache memoises one compiledProgram per distinct expression string.
var compileCache sync.Map // map[string]*compiledProgram

func compile(expr string) *compiledProgram {
	if c, ok := compileCache.Load(expr); ok {
		return c.(*compiledProgram)
	}
	// Do the polymorphic rewrite (value.ofType(Quantity) -> valueQuantity) ONCE
	// here; the generic path repeats it on every evaluation.
	expanded := expandPolymorphic(strings.TrimSpace(expr))
	chains, err := parse(expanded)
	prog := &compiledProgram{
		chains:   chains,
		parseErr: err,
		fastPath: err == nil && chainsFastPathable(chains),
	}
	compileCache.Store(expr, prog)
	return prog
}

// chainsFastPathable reports whether every node in every chain is handled by
// runChain. Unsupported kinds (and unsupported where predicates) defer to the
// generic interpreter so behaviour never diverges.
func chainsFastPathable(chains [][]node) bool {
	for _, chain := range chains {
		for i := range chain {
			switch chain[i].kind {
			case kindPath, kindWhereResolveIs, kindExists:
				// supported
			case kindWhere:
				if chain[i].whereKey == "" {
					return false // unsupported predicate — let generic drop it
				}
			default: // kindOfType (resourceType match), kindExtension
				return false
			}
		}
	}
	return true
}

// EvaluatePolymorphicCompiled is a drop-in replacement for EvaluatePolymorphic
// used by the indexer. It returns exactly the same values; only the evaluation
// strategy differs (compiled-and-cached + allocation-light, with fallback).
func EvaluatePolymorphicCompiled(expr string, resource map[string]any) ([]any, error) {
	prog := compile(expr)
	if prog.parseErr != nil {
		return nil, prog.parseErr
	}
	if !prog.fastPath {
		return evalGeneric(prog.chains, resource)
	}
	var out []any
	for _, chain := range prog.chains {
		out = runChain(chain, resource, out)
	}
	return out, nil
}

// evalGeneric runs the original node-by-node interpreter over already-parsed
// chains (identical to Evaluate's body minus parsing). Used for fallback so the
// exotic constructs keep their exact generic semantics.
func evalGeneric(chains [][]node, resource map[string]any) ([]any, error) {
	var results []any
	for _, chain := range chains {
		current := []any{resource}
		for _, n := range chain {
			next, err := evalNode(n, current)
			if err != nil {
				return nil, err
			}
			current = next
		}
		results = append(results, current...)
	}
	return results, nil
}

// runChain evaluates one chain against a single input, appending matched leaf
// values directly to out. It mirrors evalNode+applyNode+traverseField but
// without allocating an intermediate slice per node or boxing scalars.
func runChain(nodes []node, input any, out []any) []any {
	if len(nodes) == 0 {
		return append(out, input)
	}
	n := &nodes[0]
	rest := nodes[1:]
	switch n.kind {
	case kindPath:
		switch v := input.(type) {
		case map[string]any:
			if val, ok := v[n.field]; ok {
				out = runFlatten(rest, val, out)
			}
		case []any:
			for _, item := range v {
				out = runChain(nodes, item, out) // same node descends each array element
			}
		}
	case kindWhere:
		switch v := input.(type) {
		case map[string]any:
			if whereMatch(v, n) {
				out = runChain(rest, v, out)
			}
		case []any:
			for _, item := range v {
				out = runChain(nodes, item, out)
			}
		}
	case kindWhereResolveIs:
		switch v := input.(type) {
		case map[string]any:
			if ref, ok := v["reference"].(string); ok && strings.HasPrefix(ref, n.typeName+"/") {
				out = runChain(rest, v, out)
			}
		case []any:
			for _, item := range v {
				out = runChain(nodes, item, out)
			}
		}
	case kindExists:
		// exists() is a pass-through in applyNode (boolean isn't used for value
		// extraction); preserve that so results match the generic evaluator.
		out = runChain(rest, input, out)
	}
	return out
}

// runFlatten applies the remaining chain to a field value, unwrapping a
// top-level array (FHIRPath implicit iteration) without the 1-element slice
// allocation flatten() makes for scalars.
func runFlatten(rest []node, val any, out []any) []any {
	if arr, ok := val.([]any); ok {
		for _, item := range arr {
			out = runChain(rest, item, out)
		}
		return out
	}
	return runChain(rest, val, out)
}

// whereMatch implements where(key='val') / where(key!='val') with a string
// fast path, matching filterWhere's fmt.Sprintf semantics only for the rare
// non-string operands.
func whereMatch(m map[string]any, n *node) bool {
	fieldVal, ok := m[n.whereKey]
	if !ok {
		return false
	}
	var match bool
	if s, isStr := fieldVal.(string); isStr {
		match = s == n.whereVal
	} else {
		match = fmt.Sprintf("%v", fieldVal) == n.whereVal
	}
	if n.field == "!=" { // parseWhere stores the operator in field
		match = !match
	}
	return match
}
