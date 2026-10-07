// Package parser parses GQL-lite grammar version 1 into an AST.
// The first syntax error stops the parse. The message is "expected … at line:col".
package parser

import (
	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/lang/ast"
	"github.com/shekhar8352/mini-graph-db/internal/lang/lexer"
)

// Parse parses a script.
func Parse(src string) (*ast.Script, error) {
	toks, err := lexer.Scan(src)
	if err != nil {
		return nil, err
	}
	p := &Parser{toks: toks}
	s := p.script()
	if p.err != nil {
		return nil, p.err
	}
	return s, nil
}

// Parser is a recursive-descent parser over a token slice.
type Parser struct {
	toks []lexer.Token
	i    int
	err  error
}

func (p *Parser) script() *ast.Script {
	s := &ast.Script{}
	p.skipSemis()
	for p.err == nil && !p.at(lexer.EOF) {
		s.Stmts = append(s.Stmts, p.statement())
		if p.err != nil {
			return nil
		}
		if p.at(lexer.Semi) {
			p.advance()
			p.skipSemis()
			continue
		}
		if p.at(lexer.EOF) {
			break
		}
		p.fail("expected ;")
		return nil
	}
	return s
}

func (p *Parser) skipSemis() {
	for p.at(lexer.Semi) {
		p.advance()
	}
}

func (p *Parser) statement() ast.Stmt {
	switch p.cur().Kind {
	case lexer.KwBegin:
		return p.begin()
	case lexer.KwCommit:
		p.advance()
		return &ast.Commit{}
	case lexer.KwRollback:
		p.advance()
		return &ast.Rollback{}
	case lexer.KwExplain:
		p.advance()
		return &ast.Explain{Query: p.query()}
	case lexer.KwProfile:
		p.advance()
		return &ast.Profile{Query: p.query()}
	case lexer.KwVacuum:
		p.advance()
		return &ast.Vacuum{}
	case lexer.KwAnalyze:
		p.advance()
		return &ast.Analyze{}
	case lexer.KwUse:
		p.advance()
		return &ast.Use{Name: p.variable()}
	case lexer.KwTerminate:
		p.advance()
		if !p.expect(lexer.KwTransaction) {
			return nil
		}
		return &ast.Terminate{ID: p.integer()}
	case lexer.KwShow:
		return p.show()
	case lexer.KwCreate:
		return p.createStmt()
	case lexer.KwDrop:
		return p.dropStmt()
	case lexer.KwGrant:
		return p.grant()
	case lexer.KwRevoke:
		return p.revoke()
	case lexer.KwDeny:
		return p.deny()
	default:
		if p.word("ALTER") {
			return p.alterUser()
		}
		if p.clauseStart() {
			return p.query()
		}
		p.fail("expected statement")
		return nil
	}
}

func (p *Parser) createStmt() ast.Stmt {
	switch p.look().Kind {
	case lexer.KwIndex:
		return p.createIndex()
	case lexer.KwConstraint:
		return p.createConstraint()
	case lexer.KwDatabase:
		p.advance()
		p.advance()
		return &ast.CreateDatabase{Name: p.variable()}
	case lexer.KwUser:
		return p.createUser()
	case lexer.KwRole:
		p.advance()
		p.advance()
		return &ast.CreateRole{Name: p.variable()}
	case lexer.KwToken:
		return p.createToken()
	default:
		return p.query()
	}
}

func (p *Parser) dropStmt() ast.Stmt {
	switch p.look().Kind {
	case lexer.KwIndex:
		p.advance()
		p.advance()
		return &ast.DropIndex{Name: p.variable()}
	case lexer.KwConstraint:
		p.advance()
		p.advance()
		return &ast.DropConstraint{Name: p.variable()}
	case lexer.KwDatabase:
		p.advance()
		p.advance()
		name := p.variable()
		confirm := false
		if p.at(lexer.KwConfirm) {
			confirm = true
			p.advance()
		}
		return &ast.DropDatabase{Name: name, Confirm: confirm}
	case lexer.KwUser:
		p.advance()
		p.advance()
		return &ast.DropUser{Name: p.variable()}
	case lexer.KwRole:
		p.advance()
		p.advance()
		return &ast.DropRole{Name: p.variable()}
	default:
		p.fail("expected INDEX, CONSTRAINT, DATABASE, USER, or ROLE")
		return nil
	}
}

func (p *Parser) begin() ast.Stmt {
	p.advance()
	ro := false
	if p.at(lexer.KwRead) {
		p.advance()
		if !p.word("ONLY") {
			p.fail("expected ONLY")
			return nil
		}
		p.advance()
		ro = true
	}
	return &ast.Begin{ReadOnly: ro}
}

func (p *Parser) show() ast.Stmt {
	p.advance()
	sh := &ast.Show{}
	switch p.cur().Kind {
	case lexer.KwIndexes:
		sh.Kind = ast.ShowIndexes
		p.advance()
	case lexer.KwConstraints:
		sh.Kind = ast.ShowConstraints
		p.advance()
	case lexer.KwLabels:
		sh.Kind = ast.ShowLabels
		p.advance()
	case lexer.KwEdge:
		p.advance()
		if !p.expect(lexer.KwTypes) {
			return nil
		}
		sh.Kind = ast.ShowEdgeTypes
	case lexer.KwProperty:
		p.advance()
		if !p.expect(lexer.KwKeys) {
			return nil
		}
		sh.Kind = ast.ShowPropertyKeys
	case lexer.KwStats:
		sh.Kind = ast.ShowStats
		p.advance()
	case lexer.KwDatabases:
		sh.Kind = ast.ShowDatabases
		p.advance()
	case lexer.KwUsers:
		sh.Kind = ast.ShowUsers
		p.advance()
	case lexer.KwRoles:
		sh.Kind = ast.ShowRoles
		p.advance()
	case lexer.KwPrivileges:
		p.advance()
		if !p.expect(lexer.KwFor) {
			return nil
		}
		sh.Kind = ast.ShowPrivileges
		sh.Name = p.variable()
	case lexer.KwTransactions:
		sh.Kind = ast.ShowTransactions
		p.advance()
	default:
		p.fail("expected SHOW target")
		return nil
	}
	return sh
}

func (p *Parser) query() *ast.Query {
	q := &ast.Query{}
	q.Parts = append(q.Parts, p.single())
	for p.at(lexer.KwUnion) {
		p.advance()
		all := false
		if p.at(lexer.KwAll) {
			all = true
			p.advance()
		} else if p.at(lexer.KwDistinct) {
			p.advance()
		}
		q.All = append(q.All, all)
		q.Parts = append(q.Parts, p.single())
	}
	return q
}

func (p *Parser) single() *ast.SingleQuery {
	s := &ast.SingleQuery{}
	if !p.clauseStart() {
		p.fail("expected clause")
		return s
	}
	for p.clauseStart() {
		s.Clauses = append(s.Clauses, p.clause())
		if p.err != nil {
			return s
		}
	}
	return s
}

func (p *Parser) clauseStart() bool {
	switch p.cur().Kind {
	case lexer.KwOptional, lexer.KwMatch, lexer.KwUnwind, lexer.KwWith, lexer.KwReturn,
		lexer.KwCreate, lexer.KwMerge, lexer.KwSet, lexer.KwRemove, lexer.KwDelete,
		lexer.KwDetach, lexer.KwCall:
		return p.err == nil
	default:
		return false
	}
}

func (p *Parser) clause() ast.Clause {
	switch p.cur().Kind {
	case lexer.KwOptional, lexer.KwMatch:
		return p.match()
	case lexer.KwUnwind:
		return p.unwind()
	case lexer.KwWith:
		return p.with()
	case lexer.KwReturn:
		return p.ret()
	case lexer.KwCreate:
		p.advance()
		return &ast.Create{Pattern: p.pattern()}
	case lexer.KwMerge:
		return p.merge()
	case lexer.KwSet:
		return p.set()
	case lexer.KwRemove:
		return p.remove()
	case lexer.KwDelete, lexer.KwDetach:
		return p.del()
	case lexer.KwCall:
		return p.call()
	default:
		p.fail("expected clause")
		return nil
	}
}

func (p *Parser) match() *ast.Match {
	m := &ast.Match{}
	if p.at(lexer.KwOptional) {
		m.Optional = true
		p.advance()
	}
	if !p.expect(lexer.KwMatch) {
		return m
	}
	m.Pattern = p.pattern()
	m.Where = p.where()
	return m
}

func (p *Parser) where() ast.Expr {
	if !p.at(lexer.KwWhere) {
		return nil
	}
	p.advance()
	return p.expr()
}

func (p *Parser) unwind() *ast.Unwind {
	p.advance()
	e := p.expr()
	if !p.expect(lexer.KwAs) {
		return nil
	}
	return &ast.Unwind{Expr: e, Name: p.name()}
}

func (p *Parser) with() *ast.With {
	p.advance()
	return &ast.With{Body: p.returnBody(), Where: p.where()}
}

func (p *Parser) ret() *ast.Return {
	p.advance()
	return &ast.Return{Body: p.returnBody()}
}

func (p *Parser) returnBody() *ast.ReturnBody {
	b := &ast.ReturnBody{}
	if p.at(lexer.KwDistinct) {
		b.Distinct = true
		p.advance()
	}
	if p.at(lexer.Star) {
		b.Star = true
		p.advance()
	} else {
		for p.err == nil {
			item := &ast.Projection{Expr: p.expr()}
			if p.at(lexer.KwAs) {
				p.advance()
				item.As = p.name()
			}
			b.Items = append(b.Items, item)
			if !p.at(lexer.Comma) {
				break
			}
			p.advance()
		}
	}
	if p.at(lexer.KwOrder) {
		p.advance()
		if !p.expect(lexer.KwBy) {
			return b
		}
		for p.err == nil {
			s := &ast.Sort{Expr: p.expr()}
			if p.at(lexer.KwDesc) {
				s.Desc = true
				p.advance()
			} else if p.at(lexer.KwAsc) {
				p.advance()
			}
			if p.at(lexer.KwNulls) {
				p.advance()
				switch p.cur().Kind {
				case lexer.KwFirst:
					s.Nulls = ast.NullsFirst
					p.advance()
				case lexer.KwLast:
					s.Nulls = ast.NullsLast
					p.advance()
				default:
					p.fail("expected FIRST or LAST")
				}
			}
			b.Order = append(b.Order, s)
			if !p.at(lexer.Comma) {
				break
			}
			p.advance()
		}
	}
	if p.at(lexer.KwSkip) {
		p.advance()
		b.Skip = p.expr()
	}
	if p.at(lexer.KwLimit) {
		p.advance()
		b.Limit = p.expr()
	}
	return b
}

func (p *Parser) call() *ast.Call {
	p.advance()
	c := &ast.Call{Proc: []string{p.variable()}}
	for p.at(lexer.Dot) {
		p.advance()
		c.Proc = append(c.Proc, p.name())
	}
	if !p.expect(lexer.LParen) {
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
	if !p.expect(lexer.RParen) {
		return c
	}
	if p.at(lexer.KwYield) {
		p.advance()
		for p.err == nil {
			y := &ast.Yield{Name: p.name()}
			if p.at(lexer.KwAs) {
				p.advance()
				y.As = p.name()
			}
			c.Yield = append(c.Yield, y)
			if !p.at(lexer.Comma) {
				break
			}
			p.advance()
		}
	}
	c.Where = p.where()
	return c
}

func (p *Parser) merge() *ast.Merge {
	p.advance()
	m := &ast.Merge{Path: p.path()}
	for p.at(lexer.KwOn) {
		p.advance()
		switch p.cur().Kind {
		case lexer.KwCreate:
			if m.OnCreate != nil {
				p.fail("duplicate ON CREATE")
				return m
			}
			p.advance()
			m.OnCreate = p.set()
		case lexer.KwMatch:
			if m.OnMatch != nil {
				p.fail("duplicate ON MATCH")
				return m
			}
			p.advance()
			m.OnMatch = p.set()
		default:
			p.fail("expected CREATE or MATCH")
			return m
		}
	}
	return m
}

func (p *Parser) set() *ast.Set {
	if !p.expect(lexer.KwSet) {
		return nil
	}
	s := &ast.Set{}
	for p.err == nil {
		item := &ast.SetItem{Name: p.variable()}
		switch {
		case p.at(lexer.Dot):
			p.advance()
			item.Prop = p.name()
			if !p.expect(lexer.Eq) {
				return s
			}
			item.Op = ast.SetProp
			item.Value = p.expr()
		case p.at(lexer.Eq):
			p.advance()
			item.Op = ast.SetReplace
			item.Value = p.expr()
		case p.at(lexer.PlusEq):
			p.advance()
			item.Op = ast.SetPlus
			item.Value = p.expr()
		case p.at(lexer.Colon):
			item.Op = ast.SetLabels
			item.Labels = p.labels()
		default:
			p.fail("expected SET item")
			return s
		}
		s.Items = append(s.Items, item)
		if !p.at(lexer.Comma) {
			break
		}
		p.advance()
	}
	return s
}

func (p *Parser) remove() *ast.Remove {
	if !p.expect(lexer.KwRemove) {
		return nil
	}
	r := &ast.Remove{}
	for p.err == nil {
		item := &ast.RemoveItem{Name: p.variable()}
		switch {
		case p.at(lexer.Dot):
			p.advance()
			item.Prop = p.name()
		case p.at(lexer.Colon):
			item.Labels = p.labels()
		default:
			p.fail("expected property or label")
			return r
		}
		r.Items = append(r.Items, item)
		if !p.at(lexer.Comma) {
			break
		}
		p.advance()
	}
	return r
}

func (p *Parser) del() *ast.Delete {
	d := &ast.Delete{}
	if p.at(lexer.KwDetach) {
		d.Detach = true
		p.advance()
	}
	if !p.expect(lexer.KwDelete) {
		return d
	}
	for p.err == nil {
		d.Exprs = append(d.Exprs, p.expr())
		if !p.at(lexer.Comma) {
			break
		}
		p.advance()
	}
	return d
}

func (p *Parser) createIndex() *ast.CreateIndex {
	p.advance()
	p.advance()
	ix := &ast.CreateIndex{Name: p.variable()}
	if !p.expect(lexer.KwFor) {
		return ix
	}
	ix.For = p.indexFor()
	if !p.expect(lexer.KwOn) || !p.expect(lexer.LParen) {
		return ix
	}
	for p.err == nil {
		v := p.variable()
		if !p.expect(lexer.Dot) {
			return ix
		}
		ix.Props = append(ix.Props, &ast.PropRef{Var: v, Prop: p.name()})
		if !p.at(lexer.Comma) {
			break
		}
		p.advance()
	}
	p.expect(lexer.RParen)
	return ix
}

func (p *Parser) createConstraint() *ast.CreateConstraint {
	p.advance()
	p.advance()
	c := &ast.CreateConstraint{Name: p.variable()}
	if !p.expect(lexer.KwFor) {
		return c
	}
	c.For = p.indexFor()
	if !p.word("REQUIRE") {
		p.fail("expected REQUIRE")
		return c
	}
	p.advance()
	c.Var = p.variable()
	if !p.expect(lexer.Dot) || !p.ok() {
		return c
	}
	c.Prop = p.name()
	if !p.expect(lexer.KwIs) {
		return c
	}
	switch {
	case p.at(lexer.KwNot):
		p.advance()
		if !p.expect(lexer.KwNull) {
			return c
		}
		c.Kind = ast.ConstraintExists
	case p.at(lexer.ColonColon):
		p.advance()
		c.Kind = ast.ConstraintType
		c.Type = p.name()
	default:
		if !p.expect(lexer.KwUnique) {
			return c
		}
		c.Kind = ast.ConstraintUnique
	}
	return c
}

func (p *Parser) indexFor() *ast.IndexFor {
	if !p.expect(lexer.LParen) {
		return nil
	}
	if p.at(lexer.RParen) {
		p.advance()
		if !p.expect(lexer.Minus) || !p.expect(lexer.LBracket) {
			return nil
		}
		v := p.variable()
		if !p.expect(lexer.Colon) {
			return nil
		}
		on := p.name()
		if !p.expect(lexer.RBracket) || !p.expect(lexer.Minus) || !p.expect(lexer.LParen) || !p.expect(lexer.RParen) {
			return nil
		}
		return &ast.IndexFor{Edge: true, Var: v, On: on}
	}
	v := p.variable()
	if !p.expect(lexer.Colon) {
		return nil
	}
	on := p.name()
	if !p.expect(lexer.RParen) {
		return nil
	}
	return &ast.IndexFor{Var: v, On: on}
}

func (p *Parser) createUser() *ast.CreateUser {
	p.advance()
	p.advance()
	u := &ast.CreateUser{Name: p.variable()}
	p.passwordInto(&u.Password, &u.ChangeRequired)
	return u
}

func (p *Parser) alterUser() *ast.AlterUser {
	p.advance()
	if !p.expect(lexer.KwUser) {
		return nil
	}
	u := &ast.AlterUser{Name: p.variable()}
	p.passwordInto(&u.Password, &u.ChangeRequired)
	return u
}

func (p *Parser) passwordInto(pw *string, change *bool) {
	if !p.expect(lexer.KwSet) || !p.expect(lexer.KwPassword) {
		return
	}
	if !p.at(lexer.String) {
		p.fail("expected string")
		return
	}
	*pw = p.cur().Text
	p.advance()
	if p.at(lexer.KwChange) {
		p.advance()
		if p.expect(lexer.KwRequired) {
			*change = true
		}
	}
}

func (p *Parser) createToken() *ast.CreateToken {
	p.advance()
	p.advance()
	if !p.expect(lexer.KwFor) || !p.expect(lexer.KwUser) {
		return nil
	}
	t := &ast.CreateToken{User: p.variable()}
	if !p.expect(lexer.KwExpires) || !p.expect(lexer.KwIn) {
		return t
	}
	t.Expires = p.expr()
	return t
}

func (p *Parser) grant() ast.Stmt {
	p.advance()
	if p.at(lexer.KwRole) {
		p.advance()
		role := p.variable()
		if !p.expect(lexer.KwTo) {
			return nil
		}
		return &ast.GrantRole{Role: role, To: p.variable()}
	}
	priv := p.priv()
	db, who, ok := p.onDatabase(lexer.KwTo)
	if !ok {
		return nil
	}
	return &ast.GrantPriv{Priv: priv, Database: db, To: who}
}

func (p *Parser) revoke() ast.Stmt {
	p.advance()
	if p.at(lexer.KwRole) {
		p.advance()
		role := p.variable()
		if !p.expect(lexer.KwFrom) {
			return nil
		}
		return &ast.RevokeRole{Role: role, From: p.variable()}
	}
	if p.at(lexer.KwToken) {
		p.advance()
		return &ast.RevokeToken{ID: p.integer()}
	}
	priv := p.priv()
	db, who, ok := p.onDatabase(lexer.KwFrom)
	if !ok {
		return nil
	}
	return &ast.RevokePriv{Priv: priv, Database: db, From: who}
}

func (p *Parser) deny() ast.Stmt {
	p.advance()
	priv := p.priv()
	db, who, ok := p.onDatabase(lexer.KwTo)
	if !ok {
		return nil
	}
	return &ast.DenyPriv{Priv: priv, Database: db, To: who}
}

func (p *Parser) onDatabase(dir lexer.Kind) (string, string, bool) {
	if !p.expect(lexer.KwOn) || !p.expect(lexer.KwDatabase) {
		return "", "", false
	}
	db := p.variable()
	if !p.expect(dir) {
		return "", "", false
	}
	return db, p.variable(), p.err == nil
}

func (p *Parser) priv() ast.Priv {
	switch p.cur().Kind {
	case lexer.KwRead:
		p.advance()
		return ast.PrivRead
	case lexer.KwWrite:
		p.advance()
		return ast.PrivWrite
	case lexer.KwAdmin:
		p.advance()
		return ast.PrivAdmin
	default:
		p.fail("expected READ, WRITE, or ADMIN")
		return ""
	}
}

func (p *Parser) cur() lexer.Token {
	if p.i >= len(p.toks) {
		return lexer.Token{Kind: lexer.EOF}
	}
	return p.toks[p.i]
}

func (p *Parser) look() lexer.Token {
	if p.i+1 >= len(p.toks) {
		return lexer.Token{Kind: lexer.EOF}
	}
	return p.toks[p.i+1]
}

func (p *Parser) at(k lexer.Kind) bool {
	return p.err == nil && p.cur().Kind == k
}

func (p *Parser) ok() bool { return p.err == nil }

func (p *Parser) advance() {
	if p.i < len(p.toks) && p.toks[p.i].Kind != lexer.EOF {
		p.i++
	}
}

func (p *Parser) expect(k lexer.Kind) bool {
	if p.at(k) {
		p.advance()
		return true
	}
	p.fail("expected " + k.String())
	return false
}

func (p *Parser) fail(msg string) {
	if p.err != nil {
		return
	}
	t := p.cur()
	line, col := t.Line, t.Col
	if line == 0 {
		line, col = 1, 1
	}
	p.err = gerr.Newf(gerr.Syntax, "%s at %d:%d", msg, line, col)
}

func (p *Parser) variable() string {
	if !p.at(lexer.Ident) {
		p.fail("expected variable")
		return ""
	}
	s := p.cur().Text
	p.advance()
	return s
}

func (p *Parser) name() string {
	if p.cur().Kind != lexer.Ident && !p.cur().Kind.Keyword() {
		p.fail("expected name")
		return ""
	}
	s := p.cur().Text
	p.advance()
	return s
}

func (p *Parser) integer() int64 {
	if !p.at(lexer.Int) {
		p.fail("expected integer")
		return 0
	}
	n := p.cur().Int
	p.advance()
	return n
}

func (p *Parser) word(s string) bool {
	return p.at(lexer.Ident) && strings.EqualFold(p.cur().Text, s)
}
