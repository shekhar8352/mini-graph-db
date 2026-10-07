package parser

import (
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
	"github.com/shekhar8352/mini-graph-db/internal/lang/lexer"
)

func (p *Parser) pattern() *ast.Pattern {
	pat := &ast.Pattern{}
	for p.err == nil {
		pat.Paths = append(pat.Paths, p.path())
		if !p.at(lexer.Comma) {
			break
		}
		p.advance()
	}
	return pat
}

func (p *Parser) path() *ast.Path {
	path := &ast.Path{}
	if p.at(lexer.Ident) && p.look().Kind == lexer.Eq {
		path.Var = p.variable()
		p.advance()
	}
	switch {
	case p.word("shortestPath"):
		path.Fn = ast.PathShortest
		p.advance()
		if !p.expect(lexer.LParen) {
			return path
		}
	case p.word("allShortestPaths"):
		path.Fn = ast.PathAllShortest
		p.advance()
		if !p.expect(lexer.LParen) {
			return path
		}
	}
	path.Nodes = append(path.Nodes, p.node())
	for p.err == nil && (p.at(lexer.Minus) || p.at(lexer.LeftArrow)) {
		path.Rels = append(path.Rels, p.rel())
		path.Nodes = append(path.Nodes, p.node())
	}
	if path.Fn != ast.PathPlain {
		if len(path.Rels) != 1 {
			p.fail("expected one relationship")
		}
		p.expect(lexer.RParen)
	}
	return path
}

func (p *Parser) node() *ast.NodePat {
	n := &ast.NodePat{}
	if !p.expect(lexer.LParen) {
		return n
	}
	if p.at(lexer.Ident) {
		n.Name = p.variable()
	}
	n.Labels = p.labels()
	if p.at(lexer.LBrace) {
		n.Props = p.mapLit()
	}
	p.expect(lexer.RParen)
	return n
}

func (p *Parser) labels() [][]string {
	var groups [][]string
	for p.at(lexer.Colon) {
		p.advance()
		g := []string{p.name()}
		for p.at(lexer.Pipe) {
			p.advance()
			g = append(g, p.name())
		}
		groups = append(groups, g)
	}
	return groups
}

func (p *Parser) rel() *ast.RelPat {
	if p.at(lexer.LeftArrow) {
		p.advance()
		if p.at(lexer.LBracket) {
			r := p.relBody()
			r.Dir = ast.DirIn
			p.expect(lexer.Minus)
			return r
		}
		p.expect(lexer.Minus)
		return &ast.RelPat{Dir: ast.DirIn}
	}
	if !p.expect(lexer.Minus) {
		return &ast.RelPat{}
	}
	if p.at(lexer.Arrow) {
		p.advance()
		return &ast.RelPat{Dir: ast.DirOut}
	}
	if p.at(lexer.Minus) {
		p.advance()
		return &ast.RelPat{Dir: ast.DirEither}
	}
	if p.at(lexer.LBracket) {
		r := p.relBody()
		if p.at(lexer.Arrow) {
			r.Dir = ast.DirOut
			p.advance()
			return r
		}
		r.Dir = ast.DirEither
		p.expect(lexer.Minus)
		return r
	}
	p.fail("expected relationship")
	return &ast.RelPat{}
}

func (p *Parser) relBody() *ast.RelPat {
	r := &ast.RelPat{}
	if !p.expect(lexer.LBracket) {
		return r
	}
	if p.at(lexer.Ident) {
		r.Name = p.variable()
	}
	if p.at(lexer.Colon) {
		p.advance()
		r.Types = append(r.Types, p.name())
		for p.at(lexer.Pipe) {
			p.advance()
			r.Types = append(r.Types, p.name())
		}
	}
	if p.at(lexer.Star) {
		p.length(r)
	}
	if p.at(lexer.LBrace) {
		r.Props = p.mapLit()
	}
	p.expect(lexer.RBracket)
	return r
}

func (p *Parser) length(r *ast.RelPat) {
	p.advance()
	r.VarLen = true
	minSet := false
	maxSet := false
	if p.at(lexer.Int) {
		r.Min = p.cur().Int
		minSet = true
		p.advance()
	}
	if p.at(lexer.DotDot) {
		p.advance()
		if p.at(lexer.Int) {
			r.Max = p.cur().Int
			maxSet = true
			p.advance()
		}
	} else if minSet {
		r.Max = r.Min
		maxSet = true
	}
	if !minSet {
		r.Min = 1
	}
	if !maxSet {
		r.Max = -1
	}
}
