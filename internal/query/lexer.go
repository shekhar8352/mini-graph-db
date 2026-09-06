package query

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type TokenType int

const (
	TokenEOF TokenType = iota
	TokenIdent
	TokenNumber
	TokenString
	TokenLBrace
	TokenRBrace
	TokenColon
	TokenComma
	TokenMinus
	TokenArrow
	TokenEQ
	TokenNE
	TokenGT
	TokenLT
	TokenGE
	TokenLE
	TokenError
)

func (t TokenType) String() string {
	switch t {
	case TokenEOF:
		return "EOF"
	case TokenIdent:
		return "IDENT"
	case TokenNumber:
		return "NUMBER"
	case TokenString:
		return "STRING"
	case TokenLBrace:
		return "{"
	case TokenRBrace:
		return "}"
	case TokenColon:
		return ":"
	case TokenComma:
		return ","
	case TokenMinus:
		return "-"
	case TokenArrow:
		return "->"
	case TokenEQ:
		return "="
	case TokenNE:
		return "!="
	case TokenGT:
		return ">"
	case TokenLT:
		return "<"
	case TokenGE:
		return ">="
	case TokenLE:
		return "<="
	case TokenError:
		return "ERROR"
	default:
		return "?"
	}
}

type Token struct {
	Type    TokenType
	Lexeme  string
	Literal any
	Pos     int
}

type Lexer struct {
	src   string
	start int
	pos   int
}

func NewLexer(src string) *Lexer {
	return &Lexer{src: src}
}

func (l *Lexer) Next() Token {
	l.skipSpace()
	l.start = l.pos
	if l.pos >= len(l.src) {
		return l.tok(TokenEOF, nil)
	}

	r, w := utf8.DecodeRuneInString(l.src[l.pos:])
	switch {
	case r == '{':
		l.pos += w
		return l.tok(TokenLBrace, nil)
	case r == '}':
		l.pos += w
		return l.tok(TokenRBrace, nil)
	case r == ':':
		l.pos += w
		return l.tok(TokenColon, nil)
	case r == ',':
		l.pos += w
		return l.tok(TokenComma, nil)
	case r == '"':
		return l.string()
	case r == '-':
		l.pos += w
		if l.peek() == '>' {
			l.pos++
			return l.tok(TokenArrow, nil)
		}
		return l.tok(TokenMinus, nil)
	case r == '=':
		l.pos += w
		return l.tok(TokenEQ, nil)
	case r == '!':
		l.pos += w
		if l.peek() == '=' {
			l.pos++
			return l.tok(TokenNE, nil)
		}
		return Token{Type: TokenError, Lexeme: "!", Pos: l.start, Literal: fmt.Errorf("unexpected '!'")}
	case r == '>':
		l.pos += w
		if l.peek() == '=' {
			l.pos++
			return l.tok(TokenGE, nil)
		}
		return l.tok(TokenGT, nil)
	case r == '<':
		l.pos += w
		if l.peek() == '=' {
			l.pos++
			return l.tok(TokenLE, nil)
		}
		return l.tok(TokenLT, nil)
	case unicode.IsDigit(r):
		return l.number()
	case isIdentStart(r):
		return l.ident()
	default:
		l.pos += w
		return Token{
			Type:    TokenError,
			Lexeme:  string(r),
			Pos:     l.start,
			Literal: fmt.Errorf("unexpected character %q", r),
		}
	}
}

func (l *Lexer) skipSpace() {
	for l.pos < len(l.src) {
		r, w := utf8.DecodeRuneInString(l.src[l.pos:])
		if !unicode.IsSpace(r) {
			return
		}
		l.pos += w
	}
}

func (l *Lexer) peek() byte {
	if l.pos >= len(l.src) {
		return 0
	}
	return l.src[l.pos]
}

func (l *Lexer) ident() Token {
	for l.pos < len(l.src) {
		r, w := utf8.DecodeRuneInString(l.src[l.pos:])
		if !isIdentCont(r) {
			break
		}
		l.pos += w
	}
	return l.tok(TokenIdent, nil)
}

func (l *Lexer) number() Token {
	isFloat := false
	for l.pos < len(l.src) {
		r, w := utf8.DecodeRuneInString(l.src[l.pos:])
		if unicode.IsDigit(r) {
			l.pos += w
			continue
		}
		if r == '.' && !isFloat {
			isFloat = true
			l.pos += w
			continue
		}
		break
	}
	lex := l.src[l.start:l.pos]
	if isFloat {
		var f float64
		fmt.Sscanf(lex, "%f", &f)
		return l.tok(TokenNumber, f)
	}
	var n int64
	fmt.Sscanf(lex, "%d", &n)
	return l.tok(TokenNumber, n)
}

func (l *Lexer) string() Token {
	l.pos++ // opening quote
	var b strings.Builder
	for l.pos < len(l.src) {
		r, w := utf8.DecodeRuneInString(l.src[l.pos:])
		l.pos += w
		if r == '\\' {
			if l.pos >= len(l.src) {
				return Token{Type: TokenError, Pos: l.start, Literal: fmt.Errorf("unterminated string")}
			}
			esc, ew := utf8.DecodeRuneInString(l.src[l.pos:])
			l.pos += ew
			switch esc {
			case '"', '\\':
				b.WriteRune(esc)
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteRune(esc)
			}
			continue
		}
		if r == '"' {
			return l.tok(TokenString, b.String())
		}
		b.WriteRune(r)
	}
	return Token{Type: TokenError, Pos: l.start, Literal: fmt.Errorf("unterminated string")}
}

func (l *Lexer) tok(t TokenType, lit any) Token {
	return Token{Type: t, Lexeme: l.src[l.start:l.pos], Literal: lit, Pos: l.start}
}

func isIdentStart(r rune) bool {
	return unicode.IsLetter(r) || r == '_'
}

func isIdentCont(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
