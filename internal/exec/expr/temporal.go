package expr

import (
	"math/rand/v2"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/value"
)

func defaultRand() float64 { return rand.Float64() }

func fnTime(name string, args []value.Value, env *Env) (value.Value, error) {
	switch name {
	case "now":
		if env.Now != nil {
			return env.Now(), nil
		}
		out, err := value.DateTimeOffset(time.Now().UTC(), 0)
		if err != nil {
			return value.Null(), inv("datetime out of range")
		}
		return out, nil
	case "date":
		return fnDate(args[0])
	case "datetime":
		return fnDateTime(args[0])
	case "duration":
		return fnDuration(args[0])
	case "year", "month", "day", "quarter", "dayofweek":
		return fnDatePart(name, args[0])
	case "hour", "minute", "second", "millisecond", "microsecond", "nanosecond", "offsetseconds":
		return fnClockPart(name, args[0])
	default:
		return fnDurPart(name, args[0])
	}
}

func fnDate(v value.Value) (value.Value, error) {
	switch v.Kind() {
	case value.KindString:
		s, _ := v.StringValue()
		d, err := value.ParseDate(s)
		if err != nil {
			return value.Null(), inv("invalid date")
		}
		return d, nil
	case value.KindDate:
		return v, nil
	case value.KindDateTime:
		inst, _, _, _ := v.DateTimeValue()
		inst = inst.UTC()
		out, err := value.Date(inst.Year(), inst.Month(), inst.Day())
		if err != nil {
			return value.Null(), inv("date out of range")
		}
		return out, nil
	default:
		return value.Null(), inv("date expects a string, date, or datetime")
	}
}

func fnDateTime(v value.Value) (value.Value, error) {
	s, ok := v.StringValue()
	if !ok {
		return value.Null(), inv("datetime expects a string")
	}
	d, err := value.ParseDateTime(s)
	if err != nil {
		return value.Null(), inv("invalid datetime")
	}
	return d, nil
}

func fnDuration(v value.Value) (value.Value, error) {
	s, ok := v.StringValue()
	if !ok {
		return value.Null(), inv("duration expects a string")
	}
	d, err := value.ParseDuration(s)
	if err != nil {
		return value.Null(), inv("invalid duration")
	}
	return d, nil
}

func fnDatePart(name string, v value.Value) (value.Value, error) {
	y, m, d, ok := civilOf(v)
	if !ok {
		return value.Null(), inv(name + " expects a date or datetime")
	}
	switch name {
	case "year":
		return value.Int(int64(y)), nil
	case "month":
		return value.Int(int64(m)), nil
	case "day":
		return value.Int(int64(d)), nil
	case "quarter":
		return value.Int(int64((m-1)/3 + 1)), nil
	default:
		wd := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC).Weekday()
		n := int(wd)
		if n == 0 {
			n = 7
		}
		return value.Int(int64(n)), nil
	}
}

func civilOf(v value.Value) (int, int, int, bool) {
	switch v.Kind() {
	case value.KindDate:
		y, m, d, ok := v.DateValue()
		return y, int(m), d, ok
	case value.KindDateTime:
		local, _, _, ok := localTime(v)
		if !ok {
			return 0, 0, 0, false
		}
		return local.Year(), int(local.Month()), local.Day(), true
	default:
		return 0, 0, 0, false
	}
}

func fnClockPart(name string, v value.Value) (value.Value, error) {
	if v.Kind() != value.KindDateTime {
		return value.Null(), inv(name + " expects a datetime")
	}
	if name == "offsetseconds" {
		_, zoneSet, off, _ := v.DateTimeValue()
		if !zoneSet {
			return value.Null(), nil
		}
		return value.Int(int64(off)), nil
	}
	local, _, _, _ := localTime(v)
	switch name {
	case "hour":
		return value.Int(int64(local.Hour())), nil
	case "minute":
		return value.Int(int64(local.Minute())), nil
	case "second":
		return value.Int(int64(local.Second())), nil
	case "millisecond":
		return value.Int(int64(local.Nanosecond() / 1e6)), nil
	case "microsecond":
		return value.Int(int64(local.Nanosecond() / 1e3)), nil
	default:
		return value.Int(int64(local.Nanosecond())), nil
	}
}

func fnDurPart(name string, v value.Value) (value.Value, error) {
	if v.Kind() != value.KindDuration {
		return value.Null(), inv(name + " expects a duration")
	}
	months, days, nanos, _ := v.DurationValue()
	switch name {
	case "months":
		return value.Int(months), nil
	case "days":
		return value.Int(days), nil
	case "nanoseconds":
		return value.Int(nanos), nil
	case "hours":
		return value.Int(nanos / int64(time.Hour)), nil
	case "minutes":
		return value.Int(nanos / int64(time.Minute)), nil
	default:
		return value.Int(nanos / int64(time.Second)), nil
	}
}
