// Package callorder checks top-down function and method order within Go files.
package callorder

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

// checks mirrors the decorder and funcorder options whose order callorder respects.
type checks struct {
	initFirst, constructor, structMethod, alphabetical, function bool
}

// NewAnalyzer returns the call order analyzer. Its flags mirror decorder's
// init-first check and funcorder's checks, with golangci-lint's defaults.
func NewAnalyzer() *analysis.Analyzer {
	enabled := &checks{}
	analyzer := &analysis.Analyzer{
		Name: "callorder",
		Doc:  "require depth-first grouping of same-file callers and helpers",
		Run:  func(pass *analysis.Pass) (any, error) { return run(pass, *enabled) },
	}

	analyzer.Flags.BoolVar(&enabled.initFirst, "init-first", false,
		"put init functions before other functions, like decorder")
	analyzer.Flags.BoolVar(&enabled.constructor, "constructor", true,
		"keep constructors after their type and before its methods, like funcorder")
	analyzer.Flags.BoolVar(&enabled.structMethod, "struct-method", true,
		"put exported methods before unexported methods of the same type, like funcorder")
	analyzer.Flags.BoolVar(&enabled.alphabetical, "alphabetical", false,
		"sort constructors and methods of a type by name, like funcorder")
	analyzer.Flags.BoolVar(&enabled.function, "function", false,
		"put exported functions before unexported functions, like funcorder")

	return analyzer
}

func run(pass *analysis.Pass, enabled checks) (any, error) {
	for _, file := range pass.Files {
		if ast.IsGenerated(file) {
			continue
		}

		if err := checkFile(pass, file, enabled); err != nil {
			return nil, err
		}
	}

	return nil, nil
}

func checkFile(pass *analysis.Pass, file *ast.File, enabled checks) error {
	funcs, edges := callGraph(pass, file)
	rules, reasons, minimum := linterConstraints(pass, file, funcs, enabled)

	// Source order guides the depth-first walk, and constructors that cannot move
	// above their type perturb it; re-plan on the planned order until it is stable,
	// so that --fix is idempotent.
	rank := make([]int, len(funcs))
	for i := range rank {
		rank[i] = i
	}

	var order []int

	for range len(funcs) {
		order = functionOrder(funcs, edges, rules, minimum, rank)

		stable := true
		for slot, index := range order {
			stable = stable && rank[index] == slot
			rank[index] = slot
		}

		if stable {
			break
		}
	}

	for slot, index := range order {
		if slot == index {
			continue
		}

		fix, err := reorderFix(pass, file, funcs, order)
		if err != nil {
			return err
		}

		message := fmt.Sprintf("expected %s before %s in depth-first call order", funcs[index].Name.Name, funcs[slot].Name.Name)
		if reason := brokenRule(rules, reasons, index, slot); reason != "" {
			message = fmt.Sprintf("expected %s before %s: %s", funcs[index].Name.Name, funcs[slot].Name.Name, reason)
		}

		pass.Report(analysis.Diagnostic{
			Pos:            funcs[slot].Name.Pos(),
			Message:        message,
			SuggestedFixes: []analysis.SuggestedFix{fix},
		})

		break
	}

	return nil
}

// brokenRule explains why from must precede to when rules require it. The file
// places to first, so some step of the rule path runs backwards; name that rule.
func brokenRule(rules [][]int, reasons [][]string, from, to int) string {
	type step struct {
		from   int
		reason string
	}

	via := make([]*step, len(rules))
	for queue := []int{from}; len(queue) > 0 && via[to] == nil; queue = queue[1:] {
		for k, next := range rules[queue[0]] {
			if via[next] == nil {
				via[next] = &step{queue[0], reasons[queue[0]][k]}
				queue = append(queue, next)
			}
		}
	}

	for node := to; via[node] != nil && node != from; node = via[node].from {
		if via[node].from > node {
			return via[node].reason
		}
	}

	return ""
}

func callGraph(pass *analysis.Pass, file *ast.File) ([]*ast.FuncDecl, [][]int) {
	funcs := []*ast.FuncDecl{}
	indices := map[types.Object]int{}

	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			indices[pass.TypesInfo.Defs[fn.Name]] = len(funcs)
			funcs = append(funcs, fn)
		}
	}

	edges := make([][]int, len(funcs))
	for caller, fn := range funcs {
		if fn.Body == nil {
			continue
		}

		// Calls in closures belong to their enclosing declaration.
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}

			callee := typeutil.StaticCallee(pass.TypesInfo, call)
			if callee != nil {
				if index, sameFile := indices[callee.Origin()]; sameFile && !slices.Contains(edges[caller], index) {
					edges[caller] = append(edges[caller], index)
				}
			}

			return true
		})
	}

	return funcs, edges
}

// functionOrder plans which function fills each slot; rank gives the source order to follow.
func functionOrder(funcs []*ast.FuncDecl, edges, rules [][]int, minimum []token.Pos, rank []int) []int {
	graph := make([][]int, len(funcs))
	for i := range funcs {
		graph[i] = slices.Concat(edges[i], rules[i])
	}

	// Rules join components too, so the group graph stays acyclic.
	groups := components(graph)
	for _, group := range groups {
		slices.SortFunc(group, func(a, b int) int { return rank[a] - rank[b] })
	}

	slices.SortFunc(groups, func(a, b []int) int { return rank[a[0]] - rank[b[0]] })

	groupOf := make([]int, len(funcs))

	for id, group := range groups {
		for _, index := range group {
			groupOf[index] = id
		}
	}

	calls := make([][]int, len(funcs))
	groupEdges, groupRules := make([][]int, len(groups)), make([][]int, len(groups))

	for id, group := range groups {
		for _, caller := range group {
			for _, callee := range edges[caller] {
				if id != groupOf[callee] && !funcs[callee].Name.IsExported() {
					calls[caller] = append(calls[caller], callee)
					groupEdges[id] = append(groupEdges[id], groupOf[callee])
				}
			}

			for _, next := range rules[caller] {
				if id != groupOf[next] {
					groupRules[id] = append(groupRules[id], groupOf[next])
				}
			}
		}
	}

	preferred := []int{}
	for _, group := range depthFirstOrder(groupEdges, groupRules) {
		preferred = append(preferred, groups[group]...)
	}

	callsPending, rulesPending := incoming(calls), incoming(rules)
	placed := make([]bool, len(funcs))
	order := make([]int, 0, len(funcs))

	// Constructors cannot move into a function slot preceding their type, unless
	// relaxed and already there: funcorder reports those, and staying adds nothing new.
	pick := func(ignoreCalls, relaxed bool) int {
		for _, index := range preferred {
			if !placed[index] && rulesPending[index] == 0 && (ignoreCalls || callsPending[index] == 0) &&
				(minimum[index] <= funcs[len(order)].Pos() || (relaxed && minimum[index] > funcs[rank[index]].Pos())) {
				return index
			}
		}

		return -1
	}

	// ponytail: quadratic scan per file; use a priority queue if huge generated-like files matter.
	for len(order) < len(funcs) {
		// When stuck, first leave a helper above a type that a constructor below it
		// calls, then let misplaced constructors stay above their type.
		chosen := pick(false, false)
		for _, relax := range [][2]bool{{true, false}, {false, true}, {true, true}} {
			if chosen < 0 {
				chosen = pick(relax[0], relax[1])
			}
		}

		if chosen < 0 {
			// Only moving a type could satisfy the enabled funcorder checks; funcorder reports the file.
			return nil
		}

		order = append(order, chosen)

		placed[chosen] = true
		for _, next := range calls[chosen] {
			callsPending[next]--
		}

		for _, next := range rules[chosen] {
			rulesPending[next]--
		}
	}

	return order
}

// components finds strongly connected components with Tarjan's algorithm.
// Exported functions participate too: a cycle can cross the export boundary.
func components(edges [][]int) [][]int {
	groups := [][]int{}
	stack := []int{}
	index := make([]int, len(edges))
	low := make([]int, len(edges))
	onStack := make([]bool, len(edges))
	next := 0

	var visit func(int)

	visit = func(v int) {
		next++
		index[v], low[v] = next, next
		stack = append(stack, v)
		onStack[v] = true

		for _, w := range edges[v] {
			if index[w] == 0 {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], index[w])
			}
		}

		if low[v] != index[v] {
			return
		}

		group := []int{}

		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w] = false

			group = append(group, w)
			if w == v {
				break
			}
		}

		groups = append(groups, group)
	}

	for v := range edges {
		if index[v] == 0 {
			visit(v)
		}
	}

	return groups
}

// depthFirstOrder visits ready helpers in first-call order. A shared helper waits
// until every caller has been emitted; unrelated roots retain their source order.
// Rules only hold back roots, so helpers still follow their first caller.
func depthFirstOrder(edges, rules [][]int) []int {
	pending, held := incoming(edges), incoming(rules)
	order := make([]int, 0, len(edges))

	var visit func(int)

	visit = func(node int) {
		if pending[node] != 0 {
			return
		}

		pending[node] = -1

		order = append(order, node)
		for _, next := range edges[node] {
			pending[next]--
		}

		for _, next := range rules[node] {
			held[next]--
		}

		for _, next := range edges[node] {
			visit(next)
		}
	}

	for root := 0; root < len(edges); root++ {
		if pending[root] == 0 && held[root] == 0 {
			visit(root)

			root = -1 // Restart: the visit may have released an earlier root.
		}
	}

	return order
}

func incoming(edges [][]int) []int {
	counts := make([]int, len(edges))
	for _, callees := range edges {
		for _, callee := range callees {
			counts[callee]++
		}
	}

	return counts
}

// linterConstraints returns the enabled decorder and funcorder precedence rules,
// why each exists, and the earliest position each constructor can occupy.
// Rules only run from init to other functions, from exported to unexported
// declarations, from constructors to methods, or forward in name order, so they
// never form a cycle.
func linterConstraints(
	pass *analysis.Pass, file *ast.File, funcs []*ast.FuncDecl, enabled checks,
) ([][]int, [][]string, []token.Pos) {
	rules, reasons := make([][]int, len(funcs)), make([][]string, len(funcs))
	add := func(before, after int, reason string) {
		rules[before] = append(rules[before], after)
		reasons[before] = append(reasons[before], reason)
	}

	typeOf := make([]types.Object, len(funcs))
	constructor := make([]bool, len(funcs))
	minimum := make([]token.Pos, len(funcs))

	for i, fn := range funcs {
		typeOf[i], constructor[i] = funcorderType(pass, file, fn)
		if constructor[i] && enabled.constructor {
			minimum[i] = typeOf[i].Pos()
		}
	}

	isInit := func(fn *ast.FuncDecl) bool { return fn.Recv == nil && fn.Name.Name == "init" }

	for i, before := range funcs {
		for j, after := range funcs {
			sameType := i != j && typeOf[i] != nil && typeOf[i] == typeOf[j] && after.Recv != nil

			switch {
			case i == j:
			case isInit(before) && isInit(after):
				if i < j {
					add(i, j, "init functions run in source order")
				}
			case isInit(before):
				if enabled.initFirst {
					add(i, j, "init functions come first (decorder init-first)")
				}
			case sameType && constructor[i]:
				if enabled.constructor {
					add(i, j, "constructors come before their type's methods (funcorder constructor)")
				}
			case sameType && before.Recv != nil:
				if enabled.structMethod && before.Name.IsExported() && !after.Name.IsExported() {
					add(i, j, "exported methods come before unexported methods (funcorder struct-method)")
				}
			case before.Recv == nil && after.Recv == nil && !isInit(after):
				if enabled.function && before.Name.IsExported() && !after.Name.IsExported() {
					add(i, j, "exported functions come before unexported functions (funcorder function)")
				}
			}
		}
	}

	if !enabled.alphabetical {
		return rules, reasons, minimum
	}

	// funcorder sorts a type's constructors, exported methods, and unexported
	// methods separately, and only within the checks that place them.
	type set struct {
		typ  types.Object
		kind int
	}

	sorted := []int{}
	sets := make([]set, len(funcs))

	for i, fn := range funcs {
		switch {
		case typeOf[i] == nil:
			continue
		case constructor[i] && enabled.constructor:
			sets[i] = set{typeOf[i], 1}
		case fn.Recv != nil && enabled.structMethod && fn.Name.IsExported():
			sets[i] = set{typeOf[i], 2}
		case fn.Recv != nil && enabled.structMethod:
			sets[i] = set{typeOf[i], 3}
		default:
			continue
		}

		sorted = append(sorted, i)
	}

	slices.SortFunc(sorted, func(a, b int) int {
		return cmp.Or(strings.Compare(funcs[a].Name.Name, funcs[b].Name.Name), a-b)
	})

	last := map[set]int{}
	for _, i := range sorted {
		if previous, ok := last[sets[i]]; ok {
			add(previous, i, "constructors and methods are sorted by name (funcorder alphabetical)")
		}

		last[sets[i]] = i
	}

	return rules, reasons, minimum
}

// Match funcorder's same-file named types, pointer receivers, and New/Must prefixes.
func funcorderType(pass *analysis.Pass, file *ast.File, fn *ast.FuncDecl) (types.Object, bool) {
	var expr ast.Expr

	constructor := fn.Recv == nil
	if constructor {
		name := strings.ToLower(fn.Name.Name)

		prefix := (strings.HasPrefix(name, "new") && len(name) > 3) || (strings.HasPrefix(name, "must") && len(name) > 4)
		if !fn.Name.IsExported() || !prefix || fn.Type.Results.NumFields() == 0 {
			return nil, false
		}

		expr = fn.Type.Results.List[0].Type
	} else {
		expr = fn.Recv.List[0].Type
	}

	for {
		pointer, ok := expr.(*ast.StarExpr)
		if !ok {
			break
		}

		expr = pointer.X
	}

	name, ok := expr.(*ast.Ident)
	if !ok {
		return nil, false
	}

	// Like funcorder, match by name: a type parameter named T is not the type T.
	obj, ok := pass.Pkg.Scope().Lookup(name.Name).(*types.TypeName)
	if !ok || obj.Pos() < file.Pos() || obj.Pos() >= file.End() {
		return nil, false
	}

	return obj, constructor
}

// reorderFix swaps function slots only; other declarations keep their order.
// Move doc comments (including directives), bodies, and same-line trailing comments together.
func reorderFix(
	pass *analysis.Pass, file *ast.File, funcs []*ast.FuncDecl, order []int,
) (analysis.SuggestedFix, error) {
	tok := pass.Fset.File(file.Pos())

	source, err := pass.ReadFile(tok.Name())
	if err != nil {
		return analysis.SuggestedFix{}, err
	}

	// A trailing comment is safe to move only when no other declaration shares its line.
	trailing := true
	for i := 1; i < len(file.Decls); i++ {
		trailing = trailing && tok.Line(file.Decls[i-1].End()) != tok.Line(file.Decls[i].Pos())
	}

	starts, ends := make([]token.Pos, len(funcs)), make([]token.Pos, len(funcs))
	for i, fn := range funcs {
		starts[i], ends[i] = fn.Pos(), fn.End()
		if fn.Doc != nil {
			starts[i] = fn.Doc.Pos()
		}

		for _, comment := range file.Comments {
			if trailing && comment.Pos() >= ends[i] && tok.Line(comment.Pos()) == tok.Line(fn.End()) {
				ends[i] = comment.End()
			}
		}
	}

	fix := analysis.SuggestedFix{Message: "Reorder functions and their helpers"}

	for slot, index := range order {
		if slot != index {
			fix.TextEdits = append(fix.TextEdits, analysis.TextEdit{
				Pos: starts[slot], End: ends[slot], NewText: source[tok.Offset(starts[index]):tok.Offset(ends[index])],
			})
		}
	}

	return fix, nil
}
