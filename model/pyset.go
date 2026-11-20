package model

// PySet reproduces CPython's iteration order for a set of non-negative ints
// (hash(i)==i), which decides the order of some human-readable notes.
type PySet struct {
	table []int // -1 = empty
	fill  int
	order []int
}

const linearProbes = 9

func NewPySet() *PySet { return &PySet{table: newTable(8)} }

func newTable(n int) []int {
	t := make([]int, n)
	for k := range t {
		t[k] = -1
	}
	return t
}

func (s *PySet) Has(v int) bool {
	for _, x := range s.table {
		if x == v {
			return true
		}
	}
	return false
}

func insert(t []int, v int) {
	mask := len(t) - 1
	i, perturb := v&mask, uint(v)
	for {
		probes := 0
		if i+linearProbes <= mask {
			probes = linearProbes
		}
		for j := i; j <= i+probes; j++ {
			if t[j] == -1 {
				t[j] = v
				return
			}
		}
		perturb >>= 5
		i = (i*5 + 1 + int(perturb)) & mask
	}
}

// Add inserts v (no-op if present), resizing like set_add_entry.
func (s *PySet) Add(v int) {
	if s.Has(v) {
		return
	}
	insert(s.table, v)
	s.fill++
	mask := len(s.table) - 1
	if s.fill*5 < mask*3 {
		return
	}
	min := s.fill * 4
	if s.fill > 50000 {
		min = s.fill * 2
	}
	n := 8
	for n <= min {
		n <<= 1
	}
	old := s.table
	s.table = newTable(n)
	for _, x := range old {
		if x != -1 {
			insert(s.table, x)
		}
	}
}

// Items iterates in CPython order.
func (s *PySet) Items() []int {
	out := []int{}
	for _, x := range s.table {
		if x != -1 {
			out = append(out, x)
		}
	}
	return out
}

func (s *PySet) Len() int { return s.fill }
