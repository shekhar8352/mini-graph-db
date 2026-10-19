package expr

import (
	"math"
	"sort"
	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

// Agg folds one aggregate over a group.
// Add records one input. AddRow records one row for count(*).
// Value returns the group result. An empty group follows the spec.
type Agg struct {
	name     string
	distinct bool
	star     bool
	p        float64
	nullP    bool
	rows     int
	n        int
	sumI     int64
	sumF     float64
	sawFloat bool
	overflow bool
	vals     []value.Value
	seenNum  []value.Value
}

// NewAgg opens an aggregate. percentileDisc reads p once, from the second argument.
func NewAgg(name string, distinct bool, extra ...value.Value) (*Agg, error) {
	a := &Agg{name: strings.ToLower(name), distinct: distinct}
	switch a.name {
	case "count", "sum", "avg", "min", "max", "collect", "stdev":
	case "percentiledisc":
		if len(extra) != 1 {
			return nil, gerr.New(gerr.Semantic, "percentileDisc has the wrong number of arguments")
		}
		if extra[0].Kind() == value.KindNull {
			a.nullP = true
			return a, nil
		}
		f, ok := extra[0].FloatValue()
		if !ok || f < 0 || f > 1 || math.IsNaN(f) {
			return nil, inv("percentileDisc percentile is out of range")
		}
		a.p = f
	default:
		return nil, gerr.Newf(gerr.InvalidArgument, "unknown function %s", name)
	}
	return a, nil
}

// AddRow counts one row for count(*).
func (a *Agg) AddRow() error {
	if a == nil {
		return inv("missing aggregate")
	}
	if a.name != "count" || a.distinct {
		return gerr.New(gerr.Semantic, "only count(*) takes *")
	}
	a.star = true
	next, ov := addInt(int64(a.rows), 1)
	if ov {
		return inv("integer overflow")
	}
	a.rows = int(next)
	return nil
}

// Add records one value. Null inputs are skipped, except that count(*) uses AddRow.
func (a *Agg) Add(v value.Value) error {
	if a == nil {
		return inv("missing aggregate")
	}
	if a.star {
		return gerr.New(gerr.Semantic, "count(*) takes no value")
	}
	if v.Kind() == value.KindNull {
		return nil
	}
	if a.name == "sum" || a.name == "avg" || a.name == "stdev" {
		if v.Kind() != value.KindInt && v.Kind() != value.KindFloat {
			return inv("aggregate expects a number")
		}
	}
	if a.distinct && a.seen(v) {
		return nil
	}
	switch a.name {
	case "count":
		next, ov := addInt(int64(a.n), 1)
		if ov {
			return inv("integer overflow")
		}
		a.n = int(next)
		if a.distinct {
			a.vals = append(a.vals, v)
		}
	case "sum", "avg":
		return a.addNum(v)
	case "stdev":
		if err := a.addNum(v); err != nil {
			return err
		}
		a.vals = append(a.vals, v)
	default:
		a.vals = append(a.vals, v)
	}
	return nil
}

func (a *Agg) seen(v value.Value) bool {
	for _, old := range a.vals {
		if value.Equal(old, v) {
			return true
		}
	}
	for _, old := range a.seenNum {
		if value.Equal(old, v) {
			return true
		}
	}
	return false
}

func (a *Agg) addNum(v value.Value) error {
	switch v.Kind() {
	case value.KindInt:
		i, _ := v.IntValue()
		if !a.overflow {
			sum, ov := addInt(a.sumI, i)
			a.sumI = sum
			a.overflow = ov
		}
		a.sumF += float64(i)
	case value.KindFloat:
		f, _ := v.FloatValue()
		a.sawFloat = true
		a.sumF += f
	default:
		return inv("aggregate expects a number")
	}
	a.n++
	if a.distinct {
		a.seenNum = append(a.seenNum, v)
	}
	return nil
}

// Value returns the aggregate result.
func (a *Agg) Value() (value.Value, error) {
	if a == nil {
		return value.Null(), inv("missing aggregate")
	}
	if a.nullP {
		return value.Null(), nil
	}
	switch a.name {
	case "count":
		if a.star {
			return value.Int(int64(a.rows)), nil
		}
		return value.Int(int64(a.n)), nil
	case "sum":
		return a.sumValue()
	case "avg":
		return a.avgValue()
	case "min", "max":
		return a.pick()
	case "collect":
		if a.vals == nil {
			return value.List(), nil
		}
		return value.List(a.vals...), nil
	case "percentiledisc":
		return a.percentile()
	default:
		return a.stdev()
	}
}

func (a *Agg) sumValue() (value.Value, error) {
	if a.n == 0 {
		return value.Null(), nil
	}
	if !a.sawFloat {
		if a.overflow {
			return value.Null(), inv("integer overflow")
		}
		return value.Int(a.sumI), nil
	}
	if math.IsInf(a.sumF, 0) {
		return value.Null(), inv("float overflow")
	}
	return value.Float(a.sumF), nil
}

func (a *Agg) avgValue() (value.Value, error) {
	if a.n == 0 {
		return value.Null(), nil
	}
	mean := a.sumF / float64(a.n)
	if math.IsInf(mean, 0) {
		return value.Null(), inv("float overflow")
	}
	return value.Float(mean), nil
}

func (a *Agg) pick() (value.Value, error) {
	if len(a.vals) == 0 {
		return value.Null(), nil
	}
	best := a.vals[0]
	for _, v := range a.vals[1:] {
		c := value.Compare(v, best)
		if a.name == "min" && c < 0 || a.name == "max" && c > 0 {
			best = v
		}
	}
	return best, nil
}

func (a *Agg) percentile() (value.Value, error) {
	if len(a.vals) == 0 {
		return value.Null(), nil
	}
	sorted := append([]value.Value(nil), a.vals...)
	sort.Slice(sorted, func(i, j int) bool { return value.Compare(sorted[i], sorted[j]) < 0 })
	n := len(sorted)
	idx := int(math.Ceil(a.p*float64(n))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	return sorted[idx], nil
}

func (a *Agg) stdev() (value.Value, error) {
	if a.n < 2 {
		return value.Null(), nil
	}
	mean := a.sumF / float64(a.n)
	var m2 float64
	for _, v := range a.vals {
		d := asFloat(v) - mean
		m2 += d * d
	}
	out := math.Sqrt(m2 / float64(a.n-1))
	if math.IsNaN(out) || math.IsInf(out, 0) {
		return value.Null(), inv("float overflow")
	}
	return value.Float(out), nil
}
