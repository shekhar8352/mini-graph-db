package query

import (
	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func matchWhere(props map[string]value.Value, w *Where) (bool, error) {
	if w == nil {
		return true, nil
	}
	left, ok := props[w.Key]
	if !ok {
		return false, nil
	}
	right, err := value.FromAny(w.Value)
	if err != nil {
		return false, gerr.Wrap(gerr.InvalidArgument, "invalid comparison value", err)
	}
	if _, isBool := left.BoolValue(); isBool {
		if _, rightBool := right.BoolValue(); rightBool && w.Op != "=" && w.Op != "!=" {
			return false, gerr.Newf(gerr.InvalidArgument, "operator %s is not valid for booleans", w.Op)
		}
	}
	res, err := value.ApplyOp(w.Op, left, right)
	if err != nil {
		return false, gerr.Wrap(gerr.InvalidArgument, "comparison", err)
	}
	if res.Kind() == value.KindNull {
		return false, nil
	}
	b, ok := res.BoolValue()
	if !ok {
		return false, gerr.New(gerr.Internal, "comparison did not return bool")
	}
	return b, nil
}
