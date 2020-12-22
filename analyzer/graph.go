package analyzer

import "golang.org/x/tools/go/callgraph"

func GraphVisitEdgesPreOrder(g *callgraph.Graph, edge func(*callgraph.Edge) error) error {
	seen := make(map[*callgraph.Node]bool)
	var visit func(n *callgraph.Node) error
	visit = func(n *callgraph.Node) error {
		if !seen[n] {
			seen[n] = true
			for _, e := range n.Out {
				if err := edge(e); err != nil {
					return err
				}
				if err := visit(e.Callee); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, n := range g.Nodes {
		if err := visit(n); err != nil {
			return err
		}
	}
	return nil
}
