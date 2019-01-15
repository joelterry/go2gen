package main

import (
	"sort"
	"strings"
)

type cut struct {
	start int
	end   int
}

type cuts []cut

func (cs cuts) Uniq() cuts {
	var cs2 cuts
	for i, c := range cs {
		if i == 0 || c != cs[i-1] {
			cs2 = append(cs2, c)
		}
	}
	return cs2
}

func (cs cuts) Sort() {
	sort.Sort(cs)
}

func (cs cuts) Len() int {
	return len(cs)
}

func (cs cuts) Less(i, j int) bool {
	a := cs[i]
	b := cs[j]
	if a.start > b.end {
		return false
	}
	if b.start > a.end {
		return true
	}
	if a == b {
		return false
	}
	panic("overlapping cuts")
}

func (cs cuts) Swap(i, j int) {
	cs[i], cs[j] = cs[j], cs[i]
}

func (cs cuts) Apply(s string) (string, error) {
	cs.Sort()
	cs = cs.Uniq()

	var sb strings.Builder
	i := 0
	for _, c := range cs {
		_, err := sb.WriteString(s[i:c.start])
		if err != nil {
			return "", err
		}
		i = c.end
	}
	_, err := sb.WriteString(s[i:])
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}
