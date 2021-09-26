package uknow

import (
	"fmt"
	"math/rand"
)

func ShuffleIntRange(start, end int) []int {
	if end < start {
		panic(fmt.Errorf("end > start (%d > %d)", end, start))
	}

	count := end - start

	slice := make([]int, count)

	for i := 0; i < count; i++ {
		slice[i] = i
	}

	for end := len(slice); end > 0; end-- {
		randomIndex := rand.Intn(end)
		slice[randomIndex], slice[end-1] = slice[end-1], slice[randomIndex]
	}

	return slice
}

type StringSlice []string

func (s StringSlice) Len() int {
	return len(s)
}

func (s StringSlice) Less(i, j int) bool {
	return s[i] < s[j] || (s[i] == s[j] && i < j)
}

func (s StringSlice) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
