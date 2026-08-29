package retrieval

import "sort"

// Vector returns a copy of the normalized catalog vector for key. It is used by
// later retrieval facets, such as capability-centroid ranking, without exposing
// mutable index storage.
func (i *VectorIndex) Vector(key string) ([]float32, bool) {
	if i == nil {
		return nil, false
	}
	position := sort.Search(len(i.records), func(index int) bool { return i.records[index].Key >= key })
	if position >= len(i.records) || i.records[position].Key != key {
		return nil, false
	}
	return append([]float32(nil), i.records[position].Vector...), true
}
