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
	// TODO: investigate how to avoid copying the slice. The second copy is used merely to
	// convert the type of the slice.
	s := a.Sparse.AppendTo(space[:0])
	result := make([]AccessPointId, len(s))
	for i, node := range s {
		result[i] = AccessPointId(node)
	}
	return result
}