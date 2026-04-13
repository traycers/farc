package traa

func Balance(
	p []int,
) []int {
	n := len(p)
	if n == 0 {
		return []int{}
	}
	roots := make([]int, 0)
	for i, parent := range p {
		if parent == i {
			roots = append(roots, i)
		}
	}
	degree := make([]int, n)
	for _, parent := range p {
		if parent >= 0 {
			degree[parent]++
		}
	}
	offset := make([]int, n+1)
	for i := 0; i < n; i++ {
		offset[i+1] = offset[i] + degree[i]
	}
	children := make([]int, n)
	pos := make([]int, n)
	copy(pos, offset[:n])
	for i, parent := range p {
		if parent >= 0 {
			children[pos[parent]] = i
			pos[parent]++
		}
	}
	res := make([]int, n)
	stack := []int{roots[0]}
	idx := 0
	for len(stack) > 0 {
		node := stack[len(stack)] // берем последний
		res[idx] = node
		idx++
		// Добавляем детей в обратном порядке
		start := offset[node]
		end := offset[node+1]
		for i := end - 1; i >= start; i-- {
			stack = append(stack, children[i])
		}
	}
	return res
}
