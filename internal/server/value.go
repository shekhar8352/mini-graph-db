package server

import (
	"time"

	graphdbv1 "github.com/shekhar8352/mini-graph-db/api/gen/graphdb/v1"
	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/value"
)

const maxValueDepth = 32

func toProto(v value.Value) (*graphdbv1.Value, error) {
	return toProtoDepth(v, 0)
}

func toProtoDepth(v value.Value, depth int) (*graphdbv1.Value, error) {
	if depth > maxValueDepth {
		return nil, gerr.New(gerr.InvalidArgument, "value is nested too deeply")
	}
	switch v.Kind() {
	case value.KindNull:
		return &graphdbv1.Value{Kind: &graphdbv1.Value_NullValue{NullValue: &graphdbv1.Null{}}}, nil
	case value.KindBool:
		b, _ := v.BoolValue()
		return &graphdbv1.Value{Kind: &graphdbv1.Value_BoolValue{BoolValue: b}}, nil
	case value.KindInt:
		n, _ := v.IntValue()
		return &graphdbv1.Value{Kind: &graphdbv1.Value_IntValue{IntValue: n}}, nil
	case value.KindFloat:
		f, _ := v.FloatValue()
		return &graphdbv1.Value{Kind: &graphdbv1.Value_FloatValue{FloatValue: f}}, nil
	case value.KindString:
		s, _ := v.StringValue()
		return &graphdbv1.Value{Kind: &graphdbv1.Value_StringValue{StringValue: s}}, nil
	case value.KindBytes:
		b, _ := v.BytesValue()
		return &graphdbv1.Value{Kind: &graphdbv1.Value_BytesValue{BytesValue: b}}, nil
	case value.KindDate:
		y, m, d, _ := v.DateValue()
		return &graphdbv1.Value{Kind: &graphdbv1.Value_DateValue{DateValue: &graphdbv1.Date{
			Year: int32(y), Month: int32(m), Day: int32(d),
		}}}, nil
	case value.KindDateTime:
		instant, zoneSet, off, _ := v.DateTimeValue()
		return &graphdbv1.Value{Kind: &graphdbv1.Value_DatetimeValue{DatetimeValue: &graphdbv1.DateTime{
			UnixNanos: instant.UnixNano(),
			ZoneSet:   zoneSet,
			OffsetSec: int32(off),
		}}}, nil
	case value.KindDuration:
		months, days, nanos, _ := v.DurationValue()
		return &graphdbv1.Value{Kind: &graphdbv1.Value_DurationValue{DurationValue: &graphdbv1.Duration{
			Months: months, Days: days, Nanos: nanos,
		}}}, nil
	case value.KindList:
		elems, _ := v.ListValue()
		out := &graphdbv1.List{Values: make([]*graphdbv1.Value, len(elems))}
		for i, el := range elems {
			pv, err := toProtoDepth(el, depth+1)
			if err != nil {
				return nil, err
			}
			out.Values[i] = pv
		}
		return &graphdbv1.Value{Kind: &graphdbv1.Value_ListValue{ListValue: out}}, nil
	case value.KindMap:
		m, _ := v.MapValue()
		out := &graphdbv1.Map{}
		for k, el := range m {
			pv, err := toProtoDepth(el, depth+1)
			if err != nil {
				return nil, err
			}
			out.Entries = append(out.Entries, &graphdbv1.Entry{Key: k, Value: pv})
		}
		return &graphdbv1.Value{Kind: &graphdbv1.Value_MapValue{MapValue: out}}, nil
	case value.KindNode:
		id, _ := v.NodeID()
		return &graphdbv1.Value{Kind: &graphdbv1.Value_NodeValue{NodeValue: &graphdbv1.Node{Id: id}}}, nil
	case value.KindEdge:
		id, _ := v.EdgeID()
		return &graphdbv1.Value{Kind: &graphdbv1.Value_EdgeValue{EdgeValue: &graphdbv1.Edge{Id: id}}}, nil
	case value.KindPath:
		nodes, edges, _ := v.PathValue()
		return &graphdbv1.Value{Kind: &graphdbv1.Value_PathValue{PathValue: &graphdbv1.Path{
			Nodes: nodes, Edges: edges,
		}}}, nil
	default:
		return nil, gerr.Newf(gerr.Internal, "unknown value kind %s", v.Kind())
	}
}

func fromProto(v *graphdbv1.Value) (value.Value, error) {
	return fromProtoDepth(v, 0)
}

func fromProtoDepth(v *graphdbv1.Value, depth int) (value.Value, error) {
	if depth > maxValueDepth {
		return value.Value{}, gerr.New(gerr.InvalidArgument, "value is nested too deeply")
	}
	if v == nil || v.Kind == nil {
		return value.Null(), nil
	}
	switch k := v.Kind.(type) {
	case *graphdbv1.Value_NullValue:
		return value.Null(), nil
	case *graphdbv1.Value_BoolValue:
		return value.Bool(k.BoolValue), nil
	case *graphdbv1.Value_IntValue:
		return value.Int(k.IntValue), nil
	case *graphdbv1.Value_FloatValue:
		return value.Float(k.FloatValue), nil
	case *graphdbv1.Value_StringValue:
		return value.String(k.StringValue), nil
	case *graphdbv1.Value_BytesValue:
		return value.Bytes(k.BytesValue), nil
	case *graphdbv1.Value_DateValue:
		if k.DateValue == nil {
			return value.Value{}, gerr.New(gerr.InvalidArgument, "date value is empty")
		}
		got, err := value.Date(int(k.DateValue.Year), time.Month(k.DateValue.Month), int(k.DateValue.Day))
		if err != nil {
			return value.Value{}, gerr.Wrap(gerr.InvalidArgument, "date", err)
		}
		return got, nil
	case *graphdbv1.Value_DatetimeValue:
		if k.DatetimeValue == nil {
			return value.Value{}, gerr.New(gerr.InvalidArgument, "datetime value is empty")
		}
		instant := time.Unix(0, k.DatetimeValue.UnixNanos).UTC()
		var got value.Value
		var err error
		if k.DatetimeValue.ZoneSet {
			got, err = value.DateTimeOffset(instant, int(k.DatetimeValue.OffsetSec))
		} else {
			got, err = value.DateTimeUTC(instant)
		}
		if err != nil {
			return value.Value{}, gerr.Wrap(gerr.InvalidArgument, "datetime", err)
		}
		return got, nil
	case *graphdbv1.Value_DurationValue:
		if k.DurationValue == nil {
			return value.Duration(0, 0, 0), nil
		}
		d := k.DurationValue
		return value.Duration(d.Months, d.Days, d.Nanos), nil
	case *graphdbv1.Value_ListValue:
		if k.ListValue == nil {
			return value.List(), nil
		}
		elems := make([]value.Value, len(k.ListValue.Values))
		for i, el := range k.ListValue.Values {
			ev, err := fromProtoDepth(el, depth+1)
			if err != nil {
				return value.Value{}, err
			}
			elems[i] = ev
		}
		return value.List(elems...), nil
	case *graphdbv1.Value_MapValue:
		m := map[string]value.Value{}
		if k.MapValue != nil {
			for _, ent := range k.MapValue.Entries {
				if ent == nil {
					continue
				}
				ev, err := fromProtoDepth(ent.Value, depth+1)
				if err != nil {
					return value.Value{}, err
				}
				m[ent.Key] = ev
			}
		}
		return value.Map(m), nil
	case *graphdbv1.Value_NodeValue:
		if k.NodeValue == nil {
			return value.NodeRef(0), nil
		}
		return value.NodeRef(k.NodeValue.Id), nil
	case *graphdbv1.Value_EdgeValue:
		if k.EdgeValue == nil {
			return value.EdgeRef(0), nil
		}
		return value.EdgeRef(k.EdgeValue.Id), nil
	case *graphdbv1.Value_PathValue:
		var nodes, edges []uint64
		if k.PathValue != nil {
			nodes, edges = k.PathValue.Nodes, k.PathValue.Edges
		}
		got, err := value.PathOf(nodes, edges)
		if err != nil {
			return value.Value{}, gerr.Wrap(gerr.InvalidArgument, "path", err)
		}
		return got, nil
	default:
		return value.Value{}, gerr.New(gerr.InvalidArgument, "unknown value kind")
	}
}

func fromParams(in map[string]*graphdbv1.Value) (map[string]value.Value, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]value.Value, len(in))
	for k, v := range in {
		pv, err := fromProto(v)
		if err != nil {
			return nil, err
		}
		out[k] = pv
	}
	return out, nil
}
