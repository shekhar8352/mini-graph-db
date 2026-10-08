package parser

import (
	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
	"github.com/shekhar8352/mini-graph-db/internal/lang/lexer"
)

// Precedence, tightest last. Comparison does not chain.
const (
	precOr  = 1
	precXor = 2
	precAnd = 3
	precCmp = 5
	precAdd = 6
	precMul = 7
	precPow = 9
)

type led struct {
	prec  int
	right bool
	cmp   bool
}

func (p *Parser) expr() ast.Expr { return p.pratt(precOr) }

// pratt parses an expression whose operators bind at least floor.
// Prefix NOT and unary +/- are nud. Binary operators are led.
func (p *Parser) pratt(floor int) ast.Expr {
	if p.err != nil {
		return nil
	}
	left := p.prefix()
	prevCmp := false
	for p.err == nil {
		info, ok := p.ledInfo()
		if !ok || info.prec < floor {
			break
		}
		if prevCmp && info.cmp {
			p.fail("expected expression")
			return left
		}
		left = p.applyLed(left, info)
		prevCmp = info.cmp
	}
	return left
}

func (p *Parser) prefix() ast.Expr {
	switch {
	case p.at(lexer.KwNot):
		p.advance()
		return &ast.Unary{Op: "NOT", X: p.pratt(precCmp)}
	case p.at(lexer.Plus), p.at(lexer.Minus):
		op := p.cur().Kind.String()
		p.advance()
		return &ast.Unary{Op: op, X: p.pratt(precPow)}
	default:
		return p.parsePostfix()
	}
}

func (p *Parser) ledInfo() (led, bool) {
	if p.err != nil {
		return led{}, false
	}
	switch p.cur().Kind {
	case lexer.KwOr:
		return led{prec: precOr}, true
	case lexer.KwXor:
		return led{prec: precXor}, true
	case lexer.KwAnd:
		return led{prec: precAnd}, true
	case lexer.Eq, lexer.NotEq, lexer.Lt, lexer.Gt, lexer.LtEq, lexer.GtEq,
		lexer.KwIs, lexer.KwIn, lexer.KwStarts, lexer.KwEnds, lexer.KwContains:
		return led{prec: precCmp, cmp: true}, true
	case lexer.Plus, lexer.Minus:
		return led{prec: precAdd}, true
	case lexer.Star, lexer.Slash, lexer.Percent:
		return led{prec: precMul}, true
	case lexer.Caret:
		return led{prec: precPow, right: true}, true
	default:
		return led{}, false
	}
}

func (p *Parser) applyLed(left ast.Expr, info led) ast.Expr {
	if info.cmp {
		return p.compTail(left)
	}
	op := p.cur().Kind.String()
	pos := p.here()
	p.advance()
	rhsMin := info.prec + 1
	if info.right {
		rhsMin = info.prec
	}
	b := &ast.Binary{Op: op, Left: left, Right: p.pratt(rhsMin)}
	p.pinPos(b, pos)
	return b
}

func (p *Parser) compTail(left ast.Expr) ast.Expr {
	switch p.cur().Kind {
	case lexer.Eq, lexer.NotEq, lexer.Lt, lexer.Gt, lexer.LtEq, lexer.GtEq:
		op := p.cur().Kind.String()
		if p.cur().Kind == lexer.NotEq {
			op = "<>"
		}
		return p.pinBinary(op, left)
	case lexer.KwIn:
		return p.pinBinary("IN", left)
	case lexer.KwStarts:
		return p.pinBinaryWith("STARTS WITH", lexer.KwWith, left)
	case lexer.KwEnds:
		return p.pinBinaryWith("ENDS WITH", lexer.KwWith, left)
	case lexer.KwContains:
		return p.pinBinary("CONTAINS", left)
	case lexer.KwIs:
		pos := p.here()
		p.advance()
		not := false
		if p.at(lexer.KwNot) {
			not = true
			p.advance()
		}
		pred := &ast.Pred{X: left, Not: not}
		if p.at(lexer.ColonColon) {
			p.advance()
			pred.Type = p.name()
			p.pinPos(pred, pos)
			return pred
		}
		if !p.expect(lexer.KwNull) {
			return pred
		}
		p.pinPos(pred, pos)
		return pred
	default:
		return left
	}
}

func (p *Parser) pinBinary(op string, left ast.Expr) ast.Expr {
	pos := p.here()
	p.advance()
	b := &ast.Binary{Op: op, Left: left, Right: p.pratt(precAdd)}
	p.pinPos(b, pos)
	return b
}

func (p *Parser) pinBinaryWith(op string, kw lexer.Kind, left ast.Expr) ast.Expr {
	pos := p.here()
	p.advance()
	if !p.expect(kw) {
		return left
	}
	b := &ast.Binary{Op: op, Left: left, Right: p.pratt(precAdd)}
	p.pinPos(b, pos)
	return b
}

func (p *Parser) parsePostfix() ast.Expr {
	e := p.parsePrimary()
	for p.err == nil {
		switch p.cur().Kind {
		case lexer.Dot:
			pos := p.here()
			p.advance()
			prop := &ast.Property{X: e, Name: p.name()}
			p.pinPos(prop, pos)
			e = prop
		case lexer.LBracket:
			e = p.postfixIndex(e)
		default:
			return e
		}
	}
	return e
}

func (p *Parser) postfixIndex(e ast.Expr) ast.Expr {
	p.advance()
	low := p.expr()
	if p.at(lexer.DotDot) {
		p.advance()
		sl := &ast.Slice{X: e, Low: low}
		if !p.at(lexer.RBracket) {
			sl.High = p.expr()
			sl.HasHigh = true
		}
		p.expect(lexer.RBracket)
		return sl
	}
	p.expect(lexer.RBracket)
	return &ast.Index{X: e, Index: low}
}

func (p *Parser) parsePrimary() ast.Expr {
	switch p.cur().Kind {
	case lexer.KwNull:
		p.advance()
		return &ast.Literal{Kind: ast.LitNull}
	case lexer.KwTrue:
		p.advance()
		return &ast.Literal{Kind: ast.LitBool, Bool: true}
	case lexer.KwFalse:
		p.advance()
		return &ast.Literal{Kind: ast.LitBool}
	case lexer.KwNan:
		p.advance()
		return &ast.Literal{Kind: ast.LitNaN}
	case lexer.KwInf:
		p.advance()
		return &ast.Literal{Kind: ast.LitInf}
	case lexer.Int:
		n := p.cur().Int
		p.advance()
		return &ast.Literal{Kind: ast.LitInt, Int: n}
	case lexer.Float:
		n := p.cur().Float
		p.advance()
		return &ast.Literal{Kind: ast.LitFloat, Float: n}
	case lexer.String:
		s := p.cur().Text
		p.advance()
		return &ast.Literal{Kind: ast.LitString, Str: s}
	case lexer.Param:
		pos := p.here()
		s := p.cur().Text
		p.advance()
		n := &ast.Param{Name: s}
		p.pinPos(n, pos)
		return n
	case lexer.LParen:
		p.advance()
		e := p.expr()
		p.expect(lexer.RParen)
		return e
	case lexer.LBracket:
		return p.listOrComp()
	case lexer.LBrace:
		return p.mapLit()
	case lexer.KwAny, lexer.KwAll, lexer.KwNone, lexer.KwSingle:
		return p.quant()
	case lexer.KwReduce:
		return p.reduce()
	case lexer.KwCase:
		return p.caseExpr()
	default:
		return p.identOrCall()
	}
}

func (p *Parser) identOrCall() ast.Expr {
	kw := p.cur().Kind.Keyword()
	if p.cur().Kind != lexer.Ident && !kw {
		p.fail("expected expression")
		return nil
	}
	if kw && p.look().Kind != lexer.LParen {
		p.fail("expected expression")
		return nil
	}
	pos := p.here()
	name := p.name()
	var e ast.Expr
	if !p.at(lexer.LParen) {
		e = &ast.Ident{Name: name}
	} else {
		e = p.callExpr(name)
	}
	p.pinPos(e, pos)
	return e
}

func (p *Parser) callExpr(name string) ast.Expr {
	p.advance()
	c := &ast.CallExpr{Name: name}
	if p.at(lexer.KwDistinct) {
		c.Distinct = true
		p.advance()
	}
	if p.at(lexer.Star) {
		c.Star = true
		p.advance()
		p.expect(lexer.RParen)
		return c
	}
	if !p.at(lexer.RParen) {
		for p.err == nil {
			c.Args = append(c.Args, p.expr())
			if !p.at(lexer.Comma) {
				break
			}
			p.advance()
		}
	}
	p.expect(lexer.RParen)
	return c
}

func (p *Parser) listOrComp() ast.Expr {
	p.advance()
	if p.at(lexer.Ident) && p.look().Kind == lexer.KwIn {
		return p.comp()
	}
	list := &ast.List{}
	if !p.at(lexer.RBracket) {
		for p.err == nil {
			list.Elems = append(list.Elems, p.expr())
			if !p.at(lexer.Comma) {
				break
			}
			p.advance()
		}
	}
	p.expect(lexer.RBracket)
	return list
}

func (p *Parser) comp() ast.Expr {
	c := &ast.Comp{Var: p.variable()}
	if !p.expect(lexer.KwIn) {
		return c
	}
	c.In = p.expr()
	if p.at(lexer.KwWhere) {
		p.advance()
		c.Where = p.expr()
	}
	if p.at(lexer.Pipe) {
		p.advance()
		c.Proj = p.expr()
		c.HasProj = true
	}
	p.expect(lexer.RBracket)
	return c
}

func (p *Parser) mapLit() *ast.MapLit {
	if !p.expect(lexer.LBrace) {
		return nil
	}
	m := &ast.MapLit{}
	if !p.at(lexer.RBrace) {
		for p.err == nil {
			e := &ast.MapEntry{}
			if p.at(lexer.String) {
				e.Key = p.cur().Text
				e.Quoted = true
				p.advance()
			} else {
				e.Key = p.name()
			}
			if !p.expect(lexer.Colon) {
				return m
			}
			e.Value = p.expr()
			m.Entries = append(m.Entries, e)
			if !p.at(lexer.Comma) {
				break
			}
			p.advance()
		}
	}
	p.expect(lexer.RBrace)
	return m
}

func (p *Parser) quant() ast.Expr {
	q := &ast.Quant{Kind: strings.ToUpper(p.cur().Text)}
	p.advance()
	if !p.expect(lexer.LParen) {
		return q
	}
	q.Var = p.variable()
	if !p.expect(lexer.KwIn) {
		return q
	}
	q.In = p.expr()
	if !p.expect(lexer.KwWhere) {
		return q
	}
	q.Where = p.expr()
	p.expect(lexer.RParen)
	return q
}

func (p *Parser) reduce() ast.Expr {
	p.advance()
	r := &ast.Reduce{}
	if !p.expect(lexer.LParen) {
		return r
	}
	r.Acc = p.variable()
	if !p.expect(lexer.Eq) {
		return r
	}
	r.Init = p.expr()
	if !p.expect(lexer.Comma) {
		return r
	}
	r.Var = p.variable()
	if !p.expect(lexer.KwIn) {
		return r
	}
	r.In = p.expr()
	if !p.expect(lexer.Pipe) {
		return r
	}
	r.Body = p.expr()
	p.expect(lexer.RParen)
	return r
}

func (p *Parser) caseExpr() ast.Expr {
	p.advance()
	c := &ast.Case{}
	if !p.at(lexer.KwWhen) {
		c.Input = p.expr()
	}
	if !p.at(lexer.KwWhen) {
		p.fail("expected WHEN")
		return c
	}
	for p.at(lexer.KwWhen) {
		p.advance()
		w := &ast.When{Cond: p.expr()}
		if !p.expect(lexer.KwThen) {
			return c
		}
		w.Then = p.expr()
		c.Whens = append(c.Whens, w)
	}
	if p.at(lexer.KwElse) {
		p.advance()
		c.Else = p.expr()
	}
	p.expect(lexer.KwEnd)
	return c
}
