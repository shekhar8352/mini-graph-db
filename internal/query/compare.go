package query

import (
	"fmt"
	"math"
	"strings"
)

func matchWhere(props map[string]any, w *Where) (bool, error) {
	if w == nil {
		return true, nil
	}
	left, ok := props[w.Key]
	if !ok {
		return false, nil
	}
	return compare(left, w.Op, w.Value)
}

func compare(left any, op string, right any) (bool, error) {
	if ln, ok := asFloat(left); ok {
		if rn, ok := asFloat(right); ok {
			return cmpNum(ln, op, rn)
		}
	}
	if lb, ok := left.(bool); ok {
		if rb, ok := right.(bool); ok {
			switch op {
			case "=":
				return lb == rb, nil
			case "!=":
				return lb != rb, nil
			default:
				return false, fmt.Errorf("operator %s is not valid for booleans", op)
			}
		}
	}
	ls := fmt.Sprint(left)
	rs := fmt.Sprint(right)
	switch op {
	case "=":
		return ls == rs, nil
	case "!=":
		return ls != rs, nil
	case ">":
		return ls > rs, nil
	case "<":
		return ls < rs, nil
	case ">=":
		return ls >= rs, nil
	case "<=":
		return ls <= rs, nil
	default:
		return false, fmt.Errorf("unknown operator %s", op)
	}
}

func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case int64:
		return float64(t), true
	case int:
		return float64(t), true
	case float64:
		return t, true
	case string:
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(t), "%f", &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

func cmpNum(l float64, op string, r float64) (bool, error) {
	eq := math.Abs(l-r) < 1e-9
	switch op {
	case "=":
		return eq, nil
	case "!=":
		return !eq, nil
	case ">":
		return l > r && !eq, nil
	case "<":
		return l < r && !eq, nil
	case ">=":
		return l > r || eq, nil
	case "<=":
		return l < r || eq, nil
	default:
		return false, fmt.Errorf("unknown operator %s", op)
	}
}
