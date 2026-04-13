package traa

import (
	"fmt"
	"slices"
	"strings"
)

func Print[T any](
	p []int,
	d []int,
	dt []T,
) {
	size := len(p)
	if size == 0 || len(dt) == 0 {
		return
	}
	prefix := strings.Repeat(
		" ",
		slices.Max(d),
	)
	for i := range size {
		fmt.Println(
			prefix[0:d[i]],
			"-",
			dt[i],
		)
	}
}
