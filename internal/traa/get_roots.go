package traa

func getRoots(
	lst []int,
) []int {
	size := len(lst)
	res := make([]int, 0, size)
	for i := range size {
		if lst[i] == i {
			res = append(res, i)
		}
	}
	return res
}
