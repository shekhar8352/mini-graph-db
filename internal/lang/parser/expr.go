package parser

import (
	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
	"github.com/shekhar8352/mini-graph-db/internal/lang/lexer"
)

func (p *Parser) expr() ast.Expr { return p.parseOr() }

func (p *Parser) parseOr() ast.Expr {
	left := p.parseXor()
	for p.at(lexer.KwOr) {
		p.advance()
		right := p.parseXor()
		left = &ast.Binary{Op: "OR", Left: left, Right: right}
	}
	return left
}

func (p *Parser) parseXor() ast.Expr {
	left := p.parseAnd()
	for p.at(lexer.KwXor) {
		p.advance()
		right := p.parseAnd()
		left = &ast.Binary{Op: "XOR", Left: left, Right: right}
	}
	return left
}

func (p *Parser) parseAnd() ast.Expr {
	left := p.parseNot()
	for p.at(lexer.KwAnd) {
		p.advance()
		right := p.parseNot()
		left = &ast.Binary{Op: "AND", Left: left, Right: right}
	}
	return left
}

func (p *Parser) parseNot() ast.Expr {
	if p.at(lexer.KwNot) {
		p.advance()
		return &ast.Unary{Op: "NOT", X: p.parseNot()}
	}
	return p.parseComparison()
}

func (p *Parser) parseComparison() ast.Expr {
	left := p.parseAdd()
	if !p.compOp() {
		return left
	}
	return p.compTail(left)
}

func (p *Parser) compOp() bool {
	switch p.cur().Kind {
	case lexer.Eq, lexer.NotEq, lexer.Lt, lexer.Gt, lexer.LtEq, lexer.GtEq,
		lexer.KwIs, lexer.KwIn, lexer.KwStarts, lexer.KwEnds, lexer.KwContains:
		return p.err == nil
	default:
		return false
	}
}

func (p *Parser) compTail(left ast.Expr) ast.Expr {
	switch p.cur().Kind {
	case lexer.Eq, lexer.NotEq, lexer.Lt, lexer.Gt, lexer.LtEq, lexer.GtEq:
		op := p.cur().Kind.String()
		if p.cur().Kind == lexer.NotEq {
			op = "<>"
		}
		p.advance()
		return &ast.Binary{Op: op, Left: left, Right: p.parseAdd()}
	case lexer.KwIn:
		p.advance()
		return &ast.Binary{Op: "IN", Left: left, Right: p.parseAdd()}
	case lexer.KwStarts:
		p.advance()
		if !p.expect(lexer.KwWith) {
			return left
		}
		return &ast.Binary{Op: "STARTS WITH", Left: left, Right: p.parseAdd()}
	case lexer.KwEnds:
		p.advance()
		if !p.expect(lexer.KwWith) {
			return left
		}
		return &ast.Binary{Op: "ENDS WITH", Left: left, Right: p.parseAdd()}
	case lexer.KwContains:
		p.advance()
		return &ast.Binary{Op: "CONTAINS", Left: left, Right: p.parseAdd()}
	case lexer.KwIs:
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
			return pred
		}
		if !p.expect(lexer.KwNull) {
			return pred
		}
		return pred
	default:
		return left
	}
}

func (p *Parser) parseAdd() ast.Expr {
	left := p.parseMul()
	for p.at(lexer.Plus) || p.at(lexer.Minus) {
		op := p.cur().Kind.String()
		p.advance()
		right := p.parseMul()
		left = &ast.Binary{Op: op, Left: left, Right: right}
	}
	return left
}

func (p *Parser) parseMul() ast.Expr {
	left := p.parseUnary()
	for p.at(lexer.Star) || p.at(lexer.Slash) || p.at(lexer.Percent) {
		op := p.cur().Kind.String()
		p.advance()
		right := p.parseUnary()
		left = &ast.Binary{Op: op, Left: left, Right: right}
	}
	return left
}

func (p *Parser) parseUnary() ast.Expr {
	if p.at(lexer.Plus) || p.at(lexer.Minus) {
		op := p.cur().Kind.String()
		p.advance()
		return &ast.Unary{Op: op, X: p.parseUnary()}
	}
	return p.parsePow()
}

func (p *Parser) parsePow() ast.Expr {
	left := p.parsePostfix()
	if p.at(lexer.Caret) {
		p.advance()
		return &ast.Binary{Op: "^", Left: left, Right: p.parseUnary()}
	}
	return left
}

func (p *Parser) parsePostfix() ast.Expr {
	e := p.parsePrimary()
	for p.err == nil {
		switch p.cur().Kind {
		case lexer.Dot:
			p.advance()
			e = &ast.Property{X: e, Name: p.name()}
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
		s := p.cur().Text
		p.advance()
		return &ast.Param{Name: s}
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
	name := p.name()
	if !p.at(lexer.LParen) {
		return &ast.Ident{Name: name}
	}
	return p.callExpr(name)
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
