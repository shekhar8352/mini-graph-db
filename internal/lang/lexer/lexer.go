// Package lexer tokenizes GQL-lite grammar version 1.
// date, datetime, and duration are identifiers in front of a call, not literal tokens.
// A reserved word stays a keyword after '.' and ':'; the parser accepts it as a name there.
package lexer

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

// Lexer reads one GQL-lite script.
type Lexer struct {
	src  string
	i    int
	line int
	col  int
	err  error
}

// New returns a lexer over src. Invalid UTF-8 fails on the first Next call.
func New(src string) *Lexer {
	l := &Lexer{src: src, line: 1, col: 1}
	if !utf8.ValidString(src) {
		l.err = gerr.New(gerr.Syntax, "invalid UTF-8 at 1:1")
	}
	return l
}

// Scan tokenizes src, including a final EOF token.
func Scan(src string) ([]Token, error) {
	l := New(src)
	var out []Token
	for {
		tok, err := l.Next()
		if err != nil {
			return nil, err
		}
		out = append(out, tok)
		if tok.Kind == EOF {
			return out, nil
		}
	}
}

// Next returns the next token. A syntax error is sticky and carries at line:col.
func (l *Lexer) Next() (Token, error) {
	if l.err != nil {
		return Token{Kind: EOF, Line: l.line, Col: l.col, Offset: l.i}, l.err
	}
	if err := l.skip(); err != nil {
		return Token{Line: l.line, Col: l.col, Offset: l.i}, err
	}
	line, col, off := l.line, l.col, l.i
	r, w := l.peek()
	if w == 0 {
		return Token{Kind: EOF, Line: line, Col: col, Offset: off}, nil
	}
	switch {
	case r == '"':
		return l.scanString(line, col, off)
	case r == '`':
		text, err := l.scanQuoted(line, col, off)
		if err != nil {
			return Token{Line: line, Col: col, Offset: off}, err
		}
		return Token{Kind: Ident, Text: text, Line: line, Col: col, Offset: off}, nil
	case r == '$':
		return l.scanDollar(line, col, off)
	case isIdentStart(r):
		return l.scanIdentToken(line, col, off), nil
	case isDigit(r):
		return l.scanNumber(line, col, off)
	default:
		if tok, ok := l.scanOperator(line, col, off); ok {
			return tok, nil
		}
		return l.fail(line, col, off, "unexpected character "+strconv.QuoteRune(r))
	}
}

func (l *Lexer) skip() error {
	for {
		l.skipSpace()
		if l.atLineComment() {
			l.skipLineComment()
			continue
		}
		if l.atBlockComment() {
			if err := l.skipBlockComment(); err != nil {
				return err
			}
			continue
		}
		return nil
	}
}

func (l *Lexer) skipSpace() {
	for {
		r, w := l.peek()
		if w == 0 {
			return
		}
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' && l.atCRLF() {
			l.bump()
			continue
		}
		return
	}
}

func (l *Lexer) atLineComment() bool {
	if l.i+1 >= len(l.src) || l.src[l.i] != '-' || l.src[l.i+1] != '-' {
		return false
	}
	return l.i+2 >= len(l.src) || l.src[l.i+2] != '>'
}

func (l *Lexer) skipLineComment() {
	l.bump()
	l.bump()
	for {
		r, w := l.peek()
		if w == 0 || r == '\n' || r == '\r' && l.atCRLF() {
			return
		}
		l.bump()
	}
}

func (l *Lexer) atBlockComment() bool {
	return l.i+1 < len(l.src) && l.src[l.i] == '/' && l.src[l.i+1] == '*'
}

func (l *Lexer) skipBlockComment() error {
	line, col, off := l.line, l.col, l.i
	l.bump()
	l.bump()
	for {
		r, w := l.peek()
		if w == 0 {
			return l.failErr(line, col, off, "unclosed block comment")
		}
		l.bump()
		if r == '*' {
			n, nw := l.peek()
			if nw > 0 && n == '/' {
				l.bump()
				return nil
			}
		}
	}
}

func (l *Lexer) scanString(line, col, off int) (Token, error) {
	l.bump()
	for {
		r, w := l.peek()
		if w == 0 {
			return l.fail(line, col, off, "unclosed string")
		}
		if r == '\n' || r == '\r' && l.atCRLF() {
			return l.fail(line, col, off, "newline in string")
		}
		if r == '\\' {
			l.bump()
			n, nw := l.peek()
			if nw == 0 {
				return l.fail(line, col, off, "unclosed string")
			}
			if n == '\n' || n == '\r' && l.atCRLF() {
				return l.fail(line, col, off, "newline in string")
			}
			l.bump()
			continue
		}
		l.bump()
		if r == '"' {
			raw := l.src[off:l.i]
			s, err := strconv.Unquote(raw)
			if err != nil {
				return l.fail(line, col, off, "invalid escape in string")
			}
			return Token{Kind: String, Text: s, Line: line, Col: col, Offset: off}, nil
		}
	}
}

func (l *Lexer) scanQuoted(line, col, off int) (string, error) {
	l.bump()
	var b strings.Builder
	saw := false
	for {
		r, w := l.peek()
		if w == 0 {
			return "", l.failErr(line, col, off, "unclosed identifier")
		}
		if r == '\n' || r == '\r' && l.atCRLF() {
			return "", l.failErr(line, col, off, "newline in identifier")
		}
		if r == '`' {
			l.bump()
			n, nw := l.peek()
			if nw > 0 && n == '`' {
				l.bump()
				b.WriteByte('`')
				saw = true
				continue
			}
			if !saw {
				return "", l.failErr(line, col, off, "empty identifier")
			}
			return b.String(), nil
		}
		l.bump()
		b.WriteRune(r)
		saw = true
	}
}

func (l *Lexer) scanDollar(line, col, off int) (Token, error) {
	next, nw := utf8.DecodeRuneInString(l.rest(1))
	if nw > 0 && isIdentStart(next) {
		l.bump()
		name := l.rawIdent()
		return Token{Kind: Param, Text: name, Line: line, Col: col, Offset: off}, nil
	}
	if nw > 0 && next == '`' {
		l.bump()
		name, err := l.scanQuoted(line, col, off)
		if err != nil {
			return Token{Line: line, Col: col, Offset: off}, err
		}
		return Token{Kind: Param, Text: name, Line: line, Col: col, Offset: off}, nil
	}
	l.bump()
	return Token{Kind: Dollar, Text: "$", Line: line, Col: col, Offset: off}, nil
}

func (l *Lexer) scanIdentToken(line, col, off int) Token {
	text := l.rawIdent()
	if k, ok := keywords[strings.ToUpper(text)]; ok {
		return Token{Kind: k, Text: text, Line: line, Col: col, Offset: off}
	}
	return Token{Kind: Ident, Text: text, Line: line, Col: col, Offset: off}
}

func (l *Lexer) rawIdent() string {
	start := l.i
	l.bump()
	for {
		r, w := l.peek()
		if w == 0 || !isIdentCont(r) {
			break
		}
		l.bump()
	}
	return l.src[start:l.i]
}

func (l *Lexer) scanNumber(line, col, off int) (Token, error) {
	if l.src[l.i] == '0' && l.i+1 < len(l.src) && (l.src[l.i+1] == 'x' || l.src[l.i+1] == 'X') {
		l.bump()
		l.bump()
		hexAt := l.i
		for {
			r, w := l.peek()
			if w == 0 || !isHex(r) {
				break
			}
			l.bump()
		}
		if l.i == hexAt {
			return l.fail(line, col, off, "hex literal has no digits")
		}
		raw := l.src[off:l.i]
		v, err := strconv.ParseInt(l.src[hexAt:l.i], 16, 64)
		if err != nil {
			return l.fail(line, col, off, "integer out of range")
		}
		return Token{Kind: Int, Text: raw, Int: v, Line: line, Col: col, Offset: off}, nil
	}
	for {
		r, w := l.peek()
		if w == 0 || !isDigit(r) {
			break
		}
		l.bump()
	}
	floatLit := false
	if l.dotDigits() {
		floatLit = true
		l.bump()
		for {
			r, w := l.peek()
			if w == 0 || !isDigit(r) {
				break
			}
			l.bump()
		}
	}
	if l.exponent() {
		floatLit = true
		l.bump()
		r, w := l.peek()
		if w > 0 && (r == '+' || r == '-') {
			l.bump()
		}
		for {
			r, w := l.peek()
			if w == 0 || !isDigit(r) {
				break
			}
			l.bump()
		}
	}
	raw := l.src[off:l.i]
	if floatLit {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return l.fail(line, col, off, "invalid number")
		}
		return Token{Kind: Float, Text: raw, Float: v, Line: line, Col: col, Offset: off}, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return l.fail(line, col, off, "integer out of range")
	}
	return Token{Kind: Int, Text: raw, Int: v, Line: line, Col: col, Offset: off}, nil
}

func (l *Lexer) dotDigits() bool {
	if l.i >= len(l.src) || l.src[l.i] != '.' {
		return false
	}
	if l.i+1 < len(l.src) && l.src[l.i+1] == '.' {
		return false
	}
	return l.i+1 < len(l.src) && isDigit(rune(l.src[l.i+1]))
}

func (l *Lexer) exponent() bool {
	if l.i >= len(l.src) || (l.src[l.i] != 'e' && l.src[l.i] != 'E') {
		return false
	}
	j := l.i + 1
	if j < len(l.src) && (l.src[j] == '+' || l.src[j] == '-') {
		j++
	}
	return j < len(l.src) && l.src[j] >= '0' && l.src[j] <= '9'
}

func (l *Lexer) scanOperator(line, col, off int) (Token, bool) {
	if l.i+1 < len(l.src) {
		two := l.src[l.i : l.i+2]
		if k, ok := twoChar[two]; ok {
			l.bump()
			l.bump()
			return Token{Kind: k, Text: two, Line: line, Col: col, Offset: off}, true
		}
	}
	r, _ := l.peek()
	k, ok := oneChar[r]
	if !ok {
		return Token{}, false
	}
	l.bump()
	return Token{Kind: k, Text: string(r), Line: line, Col: col, Offset: off}, true
}

func (l *Lexer) peek() (rune, int) {
	if l.i >= len(l.src) {
		return 0, 0
	}
	return utf8.DecodeRuneInString(l.src[l.i:])
}

func (l *Lexer) rest(n int) string {
	if l.i+n >= len(l.src) {
		return ""
	}
	return l.src[l.i+n:]
}

func (l *Lexer) atCRLF() bool {
	r, w := l.peek()
	if r != '\r' || l.i+w >= len(l.src) {
		return false
	}
	return l.src[l.i+w] == '\n'
}

func (l *Lexer) bump() {
	r, w := l.peek()
	if w == 0 {
		return
	}
	if r == '\r' && l.i+w < len(l.src) && l.src[l.i+w] == '\n' {
		l.i += w + 1
		l.line++
		l.col = 1
		return
	}
	if r == '\n' {
		l.i += w
		l.line++
		l.col = 1
		return
	}
	l.i += w
	l.col += w
}

func (l *Lexer) fail(line, col, off int, msg string) (Token, error) {
	err := l.failErr(line, col, off, msg)
	return Token{Line: line, Col: col, Offset: off}, err
}

func (l *Lexer) failErr(line, col, off int, msg string) error {
	l.err = gerr.Newf(gerr.Syntax, "%s at %d:%d", msg, line, col)
	l.line, l.col, l.i = line, col, off
	return l.err
}

func isIdentStart(r rune) bool {
	return r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'
}

func isIdentCont(r rune) bool {
	return isIdentStart(r) || isDigit(r)
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func isHex(r rune) bool {
	return isDigit(r) || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'
}

var twoChar = map[string]Kind{
	"..": DotDot,
	"<>": NotEq,
	"!=": NotEq,
	"<=": LtEq,
	">=": GtEq,
	"::": ColonColon,
	"->": Arrow,
	"<-": LeftArrow,
	"+=": PlusEq,
}

var oneChar = map[rune]Kind{
	'(': LParen,
	')': RParen,
	'[': LBracket,
	']': RBracket,
	'{': LBrace,
	'}': RBrace,
	',': Comma,
	':': Colon,
	'.': Dot,
	';': Semi,
	'+': Plus,
	'-': Minus,
	'*': Star,
	'/': Slash,
	'%': Percent,
	'^': Caret,
	'=': Eq,
	'<': Lt,
	'>': Gt,
	'|': Pipe,
	'$': Dollar,
}
