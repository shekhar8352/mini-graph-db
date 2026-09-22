package value

import (
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Format renders v as a literal that Parse accepts.
// Map keys are emitted in sorted order. Floats that are whole numbers are
// printed with a decimal point so they parse back as floats (1.0, not 1).
func Format(v Value) string {
	var b strings.Builder
	writeFormat(&b, v)
	return b.String()
}

func writeFormat(b *strings.Builder, v Value) {
	switch v.kind {
	case KindNull:
		b.WriteString("null")
	case KindBool:
		if v.b {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case KindInt:
		b.WriteString(strconv.FormatInt(v.i, 10))
	case KindFloat:
		b.WriteString(formatFloat(v.f))
	case KindString:
		b.WriteString(strconv.Quote(v.s))
	case KindBytes:
		b.WriteString(`bytes("`)
		b.WriteString(base64.StdEncoding.EncodeToString(v.raw))
		b.WriteString(`")`)
	case KindDate:
		y, m, d := civilFromDays(v.i)
		fmt.Fprintf(b, `date("%s")`, formatYMD(y, m, d))
	case KindDateTime:
		b.WriteString(`datetime("`)
		b.WriteString(formatDateTime(v))
		b.WriteString(`")`)
	case KindDuration:
		b.WriteString(`duration("`)
		b.WriteString(formatDuration(v.i, v.day, v.nsec))
		b.WriteString(`")`)
	case KindList:
		b.WriteByte('[')
		for i, el := range v.list {
			if i > 0 {
				b.WriteString(", ")
			}
			writeFormat(b, el)
		}
		b.WriteByte(']')
	case KindMap:
		b.WriteByte('{')
		for i, e := range v.kvs {
			if i > 0 {
				b.WriteString(", ")
			}
			if isIdent(e.key) {
				b.WriteString(e.key)
			} else {
				b.WriteString(strconv.Quote(e.key))
			}
			b.WriteString(": ")
			writeFormat(b, e.val)
		}
		b.WriteByte('}')
	case KindNode:
		fmt.Fprintf(b, "node(%d)", v.u)
	case KindEdge:
		fmt.Fprintf(b, "edge(%d)", v.u)
	case KindPath:
		b.WriteString("path(")
		for i, id := range v.nodes {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(b, "node(%d)", id)
			if i < len(v.edges) {
				fmt.Fprintf(b, ", edge(%d)", v.edges[i])
			}
		}
		b.WriteByte(')')
	default:
		b.WriteString("null")
	}
}

func formatFloat(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if math.IsInf(f, 1) {
		return "Inf"
	}
	if math.IsInf(f, -1) {
		return "-Inf"
	}
	if f == 0 {
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

func formatYMD(y, m, d int) string {
	sign := ""
	if y < 0 {
		sign = "-"
		y = -y
	}
	return fmt.Sprintf("%s%04d-%02d-%02d", sign, y, m, d)
}

func formatDateTime(v Value) string {
	t := time.Unix(0, v.i).UTC()
	if v.zoneSet && v.zoneOff != 0 {
		t = t.In(time.FixedZone("", int(v.zoneOff)))
	}
	base := t.Format("2006-01-02T15:04:05")
	if t.Nanosecond() != 0 {
		frac := fmt.Sprintf(".%09d", t.Nanosecond())
		base += strings.TrimRight(frac, "0")
	}
	if !v.zoneSet {
		return base
	}
	if v.zoneOff == 0 {
		return base + "Z"
	}
	return base + formatOffset(v.zoneOff)
}

func formatOffset(sec int32) string {
	sign := '+'
	n := int(sec)
	if n < 0 {
		sign = '-'
		n = -n
	}
	hh := n / 3600
	mm := (n % 3600) / 60
	ss := n % 60
	if ss == 0 {
		return fmt.Sprintf("%c%02d:%02d", sign, hh, mm)
	}
	return fmt.Sprintf("%c%02d:%02d:%02d", sign, hh, mm, ss)
}

func formatDuration(months, days, nanos int64) string {
	if months == 0 && days == 0 && nanos == 0 {
		return "PT0S"
	}
	if months == math.MinInt64 || days == math.MinInt64 || nanos == math.MinInt64 {
		return formatDurationMixed(months, days, nanos)
	}
	neg, mixed := durationPolarity(months, days, nanos)
	if mixed {
		return formatDurationMixed(months, days, nanos)
	}
	if neg {
		months, days, nanos = -months, -days, -nanos
	}
	body := formatDurationParts(months, days, nanos, false)
	if neg {
		return "-" + body
	}
	return body
}

func durationPolarity(months, days, nanos int64) (neg, mixed bool) {
	sign := 0
	for _, n := range []int64{months, days, nanos} {
		if n == 0 {
			continue
		}
		bit := 1
		if n < 0 {
			bit = -1
		}
		if sign == 0 {
			sign = bit
			continue
		}
		if sign != bit {
			return false, true
		}
	}
	return sign < 0, false
}

func formatDurationParts(months, days, nanos int64, signed bool) string {
	var b strings.Builder
	b.WriteByte('P')
	years := months / 12
	rem := months % 12
	wroteDate := false
	if years != 0 {
		writeDurNum(&b, years, signed)
		b.WriteByte('Y')
		wroteDate = true
	}
	if rem != 0 {
		writeDurNum(&b, rem, signed)
		b.WriteByte('M')
		wroteDate = true
	}
	if days != 0 {
		writeDurNum(&b, days, signed)
		b.WriteByte('D')
		wroteDate = true
	}
	if nanos != 0 || !wroteDate {
		b.WriteByte('T')
		h := nanos / int64(time.Hour)
		nanos -= h * int64(time.Hour)
		m := nanos / int64(time.Minute)
		nanos -= m * int64(time.Minute)
		sec := nanos / int64(time.Second)
		frac := nanos % int64(time.Second)
		if h != 0 {
			writeDurNum(&b, h, signed)
			b.WriteByte('H')
		}
		if m != 0 {
			writeDurNum(&b, m, signed)
			b.WriteByte('M')
		}
		if sec != 0 || frac != 0 || (h == 0 && m == 0) {
			// A fraction with a zero whole second still needs the sign (-0.5S).
			if sec == 0 && frac != 0 && nanos < 0 {
				b.WriteByte('-')
			}
			writeDurNum(&b, sec, signed)
			if frac != 0 {
				if frac < 0 {
					frac = -frac
				}
				fs := fmt.Sprintf("%09d", frac)
				fs = strings.TrimRight(fs, "0")
				b.WriteByte('.')
				b.WriteString(fs)
			}
			b.WriteByte('S')
		}
	}
	return b.String()
}

func formatDurationMixed(months, days, nanos int64) string {
	return formatDurationParts(months, days, nanos, true)
}

func writeDurNum(b *strings.Builder, n int64, forceSign bool) {
	if forceSign && n > 0 {
		b.WriteByte('+')
	}
	b.WriteString(strconv.FormatInt(n, 10))
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	switch s {
	case "null", "true", "false", "NaN", "Inf", "date", "datetime", "duration", "bytes", "node", "edge", "path":
		return false
	default:
		return true
	}
}

// Parse reads one value literal. See docs/spec/values.md for the syntax.
func Parse(s string) (Value, error) {
	p := litParser{s: s}
	p.skip()
	v, err := p.parseValue()
	if err != nil {
		return Value{}, err
	}
	p.skip()
	if p.i != len(p.s) {
		return Value{}, fmt.Errorf("value: trailing input")
	}
	return v, nil
}

// FromAny converts a Go value used by the legacy query layer.
// nil becomes Null. Nested []any and map[string]any become lists and maps.
// Unsupported types return an error.
func FromAny(v any) (Value, error) {
	switch t := v.(type) {
	case nil:
		return Null(), nil
	case Value:
		return t, nil
	case bool:
		return Bool(t), nil
	case int:
		return Int(int64(t)), nil
	case int8:
		return Int(int64(t)), nil
	case int16:
		return Int(int64(t)), nil
	case int32:
		return Int(int64(t)), nil
	case int64:
		return Int(t), nil
	case uint:
		if uint64(t) > math.MaxInt64 {
			return Value{}, fmt.Errorf("value: uint %d overflows int64", t)
		}
		return Int(int64(t)), nil
	case uint8:
		return Int(int64(t)), nil
	case uint16:
		return Int(int64(t)), nil
	case uint32:
		return Int(int64(t)), nil
	case uint64:
		if t > math.MaxInt64 {
			return Value{}, fmt.Errorf("value: uint64 %d overflows int64", t)
		}
		return Int(int64(t)), nil
	case float32:
		return Float(float64(t)), nil
	case float64:
		return Float(t), nil
	case string:
		return String(t), nil
	case []byte:
		return Bytes(t), nil
	case []any:
		elems := make([]Value, len(t))
		for i, el := range t {
			ev, err := FromAny(el)
			if err != nil {
				return Value{}, err
			}
			elems[i] = ev
		}
		return List(elems...), nil
	case map[string]any:
		m := make(map[string]Value, len(t))
		for k, el := range t {
			ev, err := FromAny(el)
			if err != nil {
				return Value{}, err
			}
			m[k] = ev
		}
		return Map(m), nil
	default:
		return Value{}, fmt.Errorf("value: unsupported go type %T", v)
	}
}

type litParser struct {
	s string
	i int
}

func (p *litParser) skip() {
	for p.i < len(p.s) {
		r, w := utf8.DecodeRuneInString(p.s[p.i:])
		if !unicode.IsSpace(r) {
			return
		}
		p.i += w
	}
}

func (p *litParser) parseValue() (Value, error) {
	p.skip()
	if p.i >= len(p.s) {
		return Value{}, fmt.Errorf("value: unexpected end")
	}
	switch p.s[p.i] {
	case '"':
		return p.parseString()
	case '[':
		return p.parseList()
	case '{':
		return p.parseMap()
	case '-', '+', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return p.parseNumberOrInf()
	default:
		word := p.peekIdent()
		switch word {
		case "null":
			p.i += len(word)
			return Null(), nil
		case "true":
			p.i += len(word)
			return Bool(true), nil
		case "false":
			p.i += len(word)
			return Bool(false), nil
		case "NaN":
			p.i += len(word)
			return Float(math.NaN()), nil
		case "Inf":
			p.i += len(word)
			return Float(math.Inf(1)), nil
		case "date", "datetime", "duration", "bytes", "node", "edge", "path":
			p.i += len(word)
			return p.parseCall(word)
		default:
			if word == "" {
				return Value{}, fmt.Errorf("value: unexpected %q", p.s[p.i:])
			}
			return Value{}, fmt.Errorf("value: unexpected identifier %s", word)
		}
	}
}

func (p *litParser) parseNumberOrInf() (Value, error) {
	if strings.HasPrefix(p.s[p.i:], "-Inf") {
		p.i += 4
		return Float(math.Inf(-1)), nil
	}
	start := p.i
	if p.s[p.i] == '+' || p.s[p.i] == '-' {
		p.i++
	}
	if p.i >= len(p.s) || p.s[p.i] < '0' || p.s[p.i] > '9' {
		return Value{}, fmt.Errorf("value: invalid number")
	}
	for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
		p.i++
	}
	floatTok := false
	if p.i < len(p.s) && p.s[p.i] == '.' {
		floatTok = true
		p.i++
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
	}
	if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		floatTok = true
		p.i++
		if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			p.i++
		}
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
	}
	tok := p.s[start:p.i]
	if !floatTok {
		n, err := strconv.ParseInt(tok, 10, 64)
		if err != nil {
			return Value{}, fmt.Errorf("value: invalid integer %s", tok)
		}
		return Int(n), nil
	}
	f, err := strconv.ParseFloat(tok, 64)
	if err != nil {
		return Value{}, fmt.Errorf("value: invalid float %s", tok)
	}
	return Float(f), nil
}

func (p *litParser) parseString() (Value, error) {
	raw, err := p.lexQuoted()
	if err != nil {
		return Value{}, err
	}
	s, err := strconv.Unquote(raw)
	if err != nil {
		return Value{}, fmt.Errorf("value: invalid string")
	}
	return String(s), nil
}

func (p *litParser) lexQuoted() (string, error) {
	if p.i >= len(p.s) || p.s[p.i] != '"' {
		return "", fmt.Errorf("value: expected string")
	}
	q, err := strconv.QuotedPrefix(p.s[p.i:])
	if err != nil {
		return "", fmt.Errorf("value: invalid string")
	}
	p.i += len(q)
	return q, nil
}

func (p *litParser) parseList() (Value, error) {
	p.i++ // [
	p.skip()
	if p.i < len(p.s) && p.s[p.i] == ']' {
		p.i++
		return List(), nil
	}
	var elems []Value
	for {
		el, err := p.parseValue()
		if err != nil {
			return Value{}, err
		}
		elems = append(elems, el)
		p.skip()
		if p.i < len(p.s) && p.s[p.i] == ',' {
			p.i++
			continue
		}
		break
	}
	p.skip()
	if p.i >= len(p.s) || p.s[p.i] != ']' {
		return Value{}, fmt.Errorf("value: expected ]")
	}
	p.i++
	return List(elems...), nil
}

func (p *litParser) parseMap() (Value, error) {
	p.i++ // {
	p.skip()
	m := map[string]Value{}
	if p.i < len(p.s) && p.s[p.i] == '}' {
		p.i++
		return Map(m), nil
	}
	for {
		p.skip()
		var key string
		if p.i < len(p.s) && p.s[p.i] == '"' {
			sv, err := p.parseString()
			if err != nil {
				return Value{}, err
			}
			key = sv.s
		} else {
			key = p.peekIdent()
			if key == "" {
				return Value{}, fmt.Errorf("value: expected map key")
			}
			p.i += len(key)
		}
		p.skip()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return Value{}, fmt.Errorf("value: expected colon")
		}
		p.i++
		val, err := p.parseValue()
		if err != nil {
			return Value{}, err
		}
		m[key] = val
		p.skip()
		if p.i < len(p.s) && p.s[p.i] == ',' {
			p.i++
			continue
		}
		break
	}
	p.skip()
	if p.i >= len(p.s) || p.s[p.i] != '}' {
		return Value{}, fmt.Errorf("value: expected }")
	}
	p.i++
	return Map(m), nil
}

func (p *litParser) parseCall(name string) (Value, error) {
	p.skip()
	if p.i >= len(p.s) || p.s[p.i] != '(' {
		return Value{}, fmt.Errorf("value: expected ( after %s", name)
	}
	p.i++
	switch name {
	case "date", "datetime", "duration", "bytes":
		p.skip()
		sv, err := p.parseString()
		if err != nil {
			return Value{}, err
		}
		if err := p.expectClose(); err != nil {
			return Value{}, err
		}
		switch name {
		case "date":
			return ParseDate(sv.s)
		case "datetime":
			return ParseDateTime(sv.s)
		case "duration":
			return ParseDuration(sv.s)
		default:
			raw, err := base64.StdEncoding.DecodeString(sv.s)
			if err != nil {
				return Value{}, fmt.Errorf("value: invalid base64")
			}
			return Bytes(raw), nil
		}
	case "node", "edge":
		p.skip()
		n, err := p.parseUint()
		if err != nil {
			return Value{}, err
		}
		if err := p.expectClose(); err != nil {
			return Value{}, err
		}
		if name == "node" {
			return NodeRef(n), nil
		}
		return EdgeRef(n), nil
	case "path":
		return p.parsePathBody()
	default:
		return Value{}, fmt.Errorf("value: unknown call %s", name)
	}
}

func (p *litParser) parsePathBody() (Value, error) {
	p.skip()
	if p.i < len(p.s) && p.s[p.i] == ')' {
		p.i++
		return PathOf(nil, nil)
	}
	var nodes, edges []uint64
	expectNode := true
	for {
		el, err := p.parseValue()
		if err != nil {
			return Value{}, err
		}
		if expectNode {
			id, ok := el.NodeID()
			if !ok {
				return Value{}, fmt.Errorf("value: path expected a node")
			}
			nodes = append(nodes, id)
			expectNode = false
		} else {
			id, ok := el.EdgeID()
			if !ok {
				return Value{}, fmt.Errorf("value: path expected an edge")
			}
			edges = append(edges, id)
			expectNode = true
		}
		p.skip()
		if p.i < len(p.s) && p.s[p.i] == ',' {
			p.i++
			continue
		}
		break
	}
	if err := p.expectClose(); err != nil {
		return Value{}, err
	}
	if expectNode {
		return Value{}, fmt.Errorf("value: path ended on an edge")
	}
	return PathOf(nodes, edges)
}

func (p *litParser) parseUint() (uint64, error) {
	start := p.i
	if p.i >= len(p.s) || p.s[p.i] < '0' || p.s[p.i] > '9' {
		return 0, fmt.Errorf("value: expected integer")
	}
	for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
		p.i++
	}
	n, err := strconv.ParseUint(p.s[start:p.i], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("value: invalid integer")
	}
	return n, nil
}

func (p *litParser) expectClose() error {
	p.skip()
	if p.i >= len(p.s) || p.s[p.i] != ')' {
		return fmt.Errorf("value: expected )")
	}
	p.i++
	return nil
}

func (p *litParser) peekIdent() string {
	if p.i >= len(p.s) {
		return ""
	}
	r, w := utf8.DecodeRuneInString(p.s[p.i:])
	if r != '_' && !unicode.IsLetter(r) {
		return ""
	}
	j := p.i + w
	for j < len(p.s) {
		r, w = utf8.DecodeRuneInString(p.s[j:])
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			break
		}
		j += w
	}
	return p.s[p.i:j]
}
