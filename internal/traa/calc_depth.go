package traa

import "fmt"

func CalcDepth(
	p []int,
) []int {
	size := len(p)
	ds := [][]int{
		make([]int, size),
		make([]int, size),
	}
	if size == 0 {
		return []int{}
	}
	di := 0
	setValue(0, ds[0])
	setValue(1, ds[1])
	roots := getRoots(p)
	if len(roots) != 1 {
		return []int{}
	}
	setValueByIndex(
		0,
		roots,
		ds[0],
	)
	setValueByIndex(
		0,
		roots,
		ds[1],
	)
	i := -1
	for true {
		i++
		fmt.Println("i: ", i)
		di = 1 - di
		d := ds[di]
		for i := range size {
			d[i] = d[p[i]] + 1
		}
		setValueByIndex(
			0,
			roots,
			d,
		)
		if eq(ds[0], ds[1]) {
			break
		}
	}
	return ds[di]
}
