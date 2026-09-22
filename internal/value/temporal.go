package value

import (
	"fmt"
	"math"
	"time"
)

func validDate(y, m, d int) bool {
	if m < 1 || m > 12 || d < 1 || y < -9999 || y > 9999 {
		return false
	}
	return d <= daysInMonth(y, m)
}

func daysInMonth(y, m int) int {
	switch m {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if isLeap(y) {
			return 29
		}
		return 28
	default:
		return 0
	}
}

func isLeap(y int) bool {
	if y%4 != 0 {
		return false
	}
	if y%100 != 0 {
		return true
	}
	return y%400 == 0
}

func floorDiv(a, b int64) int64 {
	q := a / b
	r := a % b
	if r != 0 && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// daysFromCivil is Howard Hinnant's days_from_civil, shifted so 1970-01-01 is 0.
func daysFromCivil(y, m, d int) (int64, error) {
	yy := int64(y)
	if m <= 2 {
		yy--
	}
	era := floorDiv(yy, 400)
	yoe := yy - era*400
	var doy int64
	if m > 2 {
		doy = (153*(int64(m)-3) + 2) / 5
	} else {
		doy = (153*(int64(m)+9) + 2) / 5
	}
	doy += int64(d) - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	days := era*146097 + doe - 719468
	if days < math.MinInt32 || days > math.MaxInt32 {
		return 0, fmt.Errorf("value: date out of range")
	}
	return days, nil
}

func civilFromDays(z int64) (y, m, d int) {
	z += 719468
	era := floorDiv(z, 146097)
	doe := uint64(z - era*146097)
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y = int(int64(yoe) + era*400)
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d = int(doy - (153*mp+2)/5 + 1)
	if mp < 10 {
		m = int(mp) + 3
	} else {
		m = int(mp) - 9
		y++
	}
	return y, m, d
}

func unixNanos(t time.Time) (int64, error) {
	minT := time.Unix(0, math.MinInt64).UTC()
	maxT := time.Unix(0, math.MaxInt64).UTC()
	if t.Before(minT) || t.After(maxT) {
		return 0, fmt.Errorf("value: datetime out of range")
	}
	return t.UnixNano(), nil
}

// ParseDate parses a calendar date "YYYY-MM-DD". Years may be negative ("-0001-01-01").
func ParseDate(s string) (Value, error) {
	y, m, d, err := parseYMD(s)
	if err != nil {
		return Value{}, err
	}
	return Date(y, time.Month(m), d)
}

func parseYMD(s string) (y, m, d int, err error) {
	neg := false
	if len(s) > 0 && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		s = s[1:]
	}
	if len(s) < 10 || s[4] != '-' || s[7] != '-' {
		return 0, 0, 0, fmt.Errorf("value: invalid date %q", s)
	}
	yy, err := parseFixed(s[0:4])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("value: invalid date")
	}
	mm, err := parseFixed(s[5:7])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("value: invalid date")
	}
	dd, err := parseFixed(s[8:10])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("value: invalid date")
	}
	if len(s) != 10 {
		return 0, 0, 0, fmt.Errorf("value: invalid date")
	}
	if neg {
		yy = -yy
	}
	return yy, mm, dd, nil
}

func parseFixed(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, fmt.Errorf("value: empty number")
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("value: invalid number")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// ParseDateTime parses an RFC 3339 timestamp.
// A trailing Z or numeric offset records the zone. A bare clock time is a UTC
// instant with no zone recorded.
func ParseDateTime(s string) (Value, error) {
	if s == "" {
		return Value{}, fmt.Errorf("value: empty datetime")
	}
	zoneAt := zoneSuffix(s)
	var off int
	hasZone := zoneAt >= 0
	body := s
	if hasZone {
		if s[zoneAt] == 'Z' || s[zoneAt] == 'z' {
			if zoneAt != len(s)-1 {
				return Value{}, fmt.Errorf("value: invalid datetime %q", s)
			}
			off = 0
			body = s[:zoneAt]
		} else {
			sec, err := parseOffset(s[zoneAt:])
			if err != nil {
				return Value{}, err
			}
			off = sec
			body = s[:zoneAt]
		}
	}
	t, err := parseClockAsUTC(body)
	if err != nil {
		return Value{}, err
	}
	if !hasZone {
		return DateTimeUTC(t)
	}
	// body is the local clock. The UTC instant is local minus the offset.
	instant := t.Add(-time.Duration(off) * time.Second)
	return DateTimeOffset(instant, off)
}

func zoneSuffix(s string) int {
	if len(s) == 0 {
		return -1
	}
	if s[len(s)-1] == 'Z' || s[len(s)-1] == 'z' {
		return len(s) - 1
	}
	// Look for a +HH:MM or -HH:MM that is not the year sign.
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '+' || s[i] == '-' {
			if i >= 10 && i+1 < len(s) {
				return i
			}
			return -1
		}
		if s[i] == 'T' || s[i] == 't' {
			return -1
		}
	}
	return -1
}

func parseOffset(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("value: invalid zone offset")
	}
	var sign int
	switch s[0] {
	case '+':
		sign = 1
	case '-':
		sign = -1
	default:
		return 0, fmt.Errorf("value: invalid zone offset")
	}
	rest := s[1:]
	// HH:MM or HH:MM:SS
	if len(rest) != 5 && len(rest) != 8 {
		return 0, fmt.Errorf("value: invalid zone offset")
	}
	if rest[2] != ':' {
		return 0, fmt.Errorf("value: invalid zone offset")
	}
	hh, err := parseFixed(rest[0:2])
	if err != nil {
		return 0, fmt.Errorf("value: invalid zone offset")
	}
	mm, err := parseFixed(rest[3:5])
	if err != nil {
		return 0, fmt.Errorf("value: invalid zone offset")
	}
	ss := 0
	if len(rest) == 8 {
		if rest[5] != ':' {
			return 0, fmt.Errorf("value: invalid zone offset")
		}
		ss, err = parseFixed(rest[6:8])
		if err != nil {
			return 0, fmt.Errorf("value: invalid zone offset")
		}
	}
	if hh > 18 || mm > 59 || ss > 59 {
		return 0, fmt.Errorf("value: invalid zone offset")
	}
	sec := sign * (hh*3600 + mm*60 + ss)
	if sec < -18*3600 || sec > 18*3600 {
		return 0, fmt.Errorf("value: zone offset out of range")
	}
	return sec, nil
}

func parseClockAsUTC(s string) (time.Time, error) {
	layouts := []string{
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02t15:04:05.999999999",
		"2006-01-02t15:04:05",
	}
	for _, layout := range layouts {
		t, err := time.ParseInLocation(layout, s, time.UTC)
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("value: invalid datetime %q", s)
}

// ParseDuration parses an ISO 8601 duration such as "P1DT2H" or "P1Y2M3DT4H5M6.5S".
// A leading minus negates every component. Weeks (W) convert to days and may not
// be combined with Y, M, or D. One year is twelve months. Hours, minutes, and
// seconds become nanoseconds. A fraction is allowed only on seconds.
func ParseDuration(s string) (Value, error) {
	if s == "" {
		return Value{}, fmt.Errorf("value: empty duration")
	}
	neg := false
	if s[0] == '-' || s[0] == '+' {
		neg = s[0] == '-'
		s = s[1:]
	}
	if len(s) == 0 || (s[0] != 'P' && s[0] != 'p') {
		return Value{}, fmt.Errorf("value: duration must start with P")
	}
	s = s[1:]
	if s == "" {
		return Value{}, fmt.Errorf("value: empty duration")
	}

	var years, months, weeks, days int64
	var hours, mins, nanos int64
	seenTime := false
	seenAny := false
	seenWeek := false
	seenDate := false

	for len(s) > 0 {
		if s[0] == 'T' || s[0] == 't' {
			if seenTime {
				return Value{}, fmt.Errorf("value: repeated T in duration")
			}
			seenTime = true
			s = s[1:]
			if s == "" {
				return Value{}, fmt.Errorf("value: empty time in duration")
			}
			continue
		}
		compNeg := false
		if s[0] == '+' || s[0] == '-' {
			compNeg = s[0] == '-'
			s = s[1:]
		}
		whole, fracDigits, rest, err := takeDurationNumber(s)
		if err != nil {
			return Value{}, err
		}
		sign := int64(1)
		if compNeg {
			sign = -1
		}
		if rest == "" {
			return Value{}, fmt.Errorf("value: duration missing designator")
		}
		des := rest[0]
		rest = rest[1:]
		if fracDigits != "" && des != 'S' && des != 's' {
			return Value{}, fmt.Errorf("value: fractional duration only allowed on seconds")
		}
		seenAny = true
		switch des {
		case 'Y', 'y':
			if seenTime || seenWeek {
				return Value{}, fmt.Errorf("value: misplaced Y in duration")
			}
			years, err = addChecked(years, sign*whole)
			seenDate = true
		case 'M', 'm':
			if seenTime {
				mins, err = addChecked(mins, sign*whole)
			} else {
				if seenWeek {
					return Value{}, fmt.Errorf("value: weeks cannot mix with months")
				}
				months, err = addChecked(months, sign*whole)
				seenDate = true
			}
		case 'W', 'w':
			if seenTime || seenDate {
				return Value{}, fmt.Errorf("value: weeks cannot mix with other date components")
			}
			weeks, err = addChecked(weeks, sign*whole)
			seenWeek = true
		case 'D', 'd':
			if seenTime || seenWeek {
				return Value{}, fmt.Errorf("value: misplaced D in duration")
			}
			days, err = addChecked(days, sign*whole)
			seenDate = true
		case 'H', 'h':
			if !seenTime {
				return Value{}, fmt.Errorf("value: H requires T")
			}
			hours, err = addChecked(hours, sign*whole)
		case 'S', 's':
			if !seenTime {
				return Value{}, fmt.Errorf("value: S requires T")
			}
			var ns int64
			ns, err = secondsToNanos(whole, fracDigits)
			if err == nil && sign < 0 {
				ns = -ns
			}
			if err == nil {
				nanos, err = addChecked(nanos, ns)
			}
		default:
			return Value{}, fmt.Errorf("value: unknown duration designator %c", des)
		}
		if err != nil {
			return Value{}, err
		}
		s = rest
	}
	if !seenAny {
		return Value{}, fmt.Errorf("value: empty duration")
	}

	totalMonths, err := scaleAdd(years, 12, months)
	if err != nil {
		return Value{}, err
	}
	weekDays, err := mulChecked(weeks, 7)
	if err != nil {
		return Value{}, err
	}
	totalDays, err := addChecked(days, weekDays)
	if err != nil {
		return Value{}, err
	}
	totalNanos := nanos
	if hours != 0 {
		h, err := mulChecked(hours, int64(time.Hour))
		if err != nil {
			return Value{}, err
		}
		totalNanos, err = addChecked(totalNanos, h)
		if err != nil {
			return Value{}, err
		}
	}
	if mins != 0 {
		m, err := mulChecked(mins, int64(time.Minute))
		if err != nil {
			return Value{}, err
		}
		totalNanos, err = addChecked(totalNanos, m)
		if err != nil {
			return Value{}, err
		}
	}
	if neg {
		totalMonths = -totalMonths
		totalDays = -totalDays
		totalNanos = -totalNanos
	}
	return Duration(totalMonths, totalDays, totalNanos), nil
}

func takeDurationNumber(s string) (whole int64, frac string, rest string, err error) {
	if s == "" || s[0] < '0' || s[0] > '9' {
		return 0, "", "", fmt.Errorf("value: expected number in duration")
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	whole, err = parseInt64(s[:i])
	if err != nil {
		return 0, "", "", err
	}
	s = s[i:]
	if len(s) > 0 && s[0] == '.' {
		j := 1
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j == 1 {
			return 0, "", "", fmt.Errorf("value: expected fraction in duration")
		}
		frac = s[1:j]
		s = s[j:]
	}
	return whole, frac, s, nil
}

func parseInt64(s string) (int64, error) {
	var v int64
	for _, c := range s {
		d := int64(c - '0')
		if v > (math.MaxInt64-d)/10 {
			return 0, fmt.Errorf("value: duration number overflow")
		}
		v = v*10 + d
	}
	return v, nil
}

func secondsToNanos(whole int64, frac string) (int64, error) {
	n, err := mulChecked(whole, int64(time.Second))
	if err != nil {
		return 0, err
	}
	if frac == "" {
		return n, nil
	}
	if len(frac) > 9 {
		frac = frac[:9]
	}
	var digits int64
	for _, c := range frac {
		digits = digits*10 + int64(c-'0')
	}
	for i := len(frac); i < 9; i++ {
		digits *= 10
	}
	return addChecked(n, digits)
}

func addChecked(a, b int64) (int64, error) {
	c := a + b
	if (b > 0 && c < a) || (b < 0 && c > a) {
		return 0, fmt.Errorf("value: duration overflow")
	}
	return c, nil
}

func mulChecked(a, b int64) (int64, error) {
	if a == 0 || b == 0 {
		return 0, nil
	}
	c := a * b
	if c/b != a {
		return 0, fmt.Errorf("value: duration overflow")
	}
	return c, nil
}

func scaleAdd(count, scale, extra int64) (int64, error) {
	scaled, err := mulChecked(count, scale)
	if err != nil {
		return 0, err
	}
	return addChecked(scaled, extra)
}
