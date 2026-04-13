package traa

type Tree[T any] struct {
	Parent []int
	Order  []int
	Depth  []int
	Data   []T
}
