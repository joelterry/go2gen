package main

import "strings"

func capitalize(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[0:1]) + s[1:]
}

func typeToVar(t string) string {
	switch {
	case len(t) > 4 && t[0:4] == "map[":
		return mapTypeToVar(t)
	case len(t) > 2 && t[0:2] == "[]":
		return "slc" + capitalize(typeToVar(t[2:]))
	case len(t) > 1 && t[0] == '[':
		return arrTypeToVar(t)
	case len(t) > 1 && t[0] == '*':
		return "ptr" + capitalize(typeToVar(t[1:]))
	default:
		// leave out package name
		dot := strings.Index(t, ".")
		if dot < 0 {
			return t
		}
		return typeToVar(t[dot+1:])
	}
}

func mapTypeToVar(t string) string {
	t = t[4:]
	bracks := 0
	for i, c := range t {
		switch c {
		case '[':
			bracks++
		case ']':
			if bracks == 0 {
				key := capitalize(typeToVar(t[:i]))
				val := capitalize(typeToVar(t[i+1:]))
				return "mapOf" + key + "To" + val
			}
			bracks--
		}
	}
	panic("invalid map type: " + t)
}

func arrTypeToVar(t string) string {
	for i, c := range t {
		if c == ']' {
			return "arr" + capitalize(typeToVar(t[i+1:]))
		}
	}
	panic("invalid arr type: " + t)
}
