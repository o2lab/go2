package pointer

type AccessPointId = nodeid
type AccessPointSet = nodeset

func (p *Pointer) AccessPointSet() *AccessPointSet {
	if p.n == 0 {
		return nil
	}
	return &p.a.nodes[p.n].solve.pts
}

func (a *AccessPointSet) ToSlice() []AccessPointId {
	var space [50]int
	s := a.Sparse.AppendTo(space[:0])
	result := make([]AccessPointId, len(s))
	for _, node := range s {
		result = append(result, AccessPointId(node))
	}
	return result
}