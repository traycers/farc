package traa

func New[T any]() *Tree[T] {
	return &Tree[T]{
		Parent: []int{},
		Order:  []int{},
		Data:   []T{},
	}
}
